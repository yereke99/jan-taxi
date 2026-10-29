"""Bounded offline PP-OCRv5 adapter. Only the parent Go worker consumes stdout.

Models are installed and hash-checked at image build time. This process has no
network access in Compose. Never log recognized text, filenames or exceptions.
"""
import contextlib
import json
import os
from pathlib import Path
import sys
import re
import unicodedata

os.environ["OMP_NUM_THREADS"] = "1"
os.environ["OPENBLAS_NUM_THREADS"] = "1"
os.environ["OPENCV_IO_MAX_IMAGE_PIXELS"] = "4100000"

# Identical Latin/Cyrillic glyphs share model probability mass in Cyrillic name
# fields. Distinct Kazakh letters (Қ/К, Ә/А, Ұ/У, І/И) remain separate. This
# operation never uses the entered name, and never applies to document numbers.
ALIASES = {"A":"А", "B":"В", "C":"С", "E":"Е", "H":"Н", "K":"К", "M":"М", "O":"О", "P":"Р", "T":"Т", "X":"Х", "I":"І", "a":"а", "c":"с", "e":"е", "o":"о", "p":"р", "x":"х", "i":"і"}
NAME_LABEL = re.compile(r"(?:ФАМИЛИЯ|ТЕГІ|ОТЧЕСТВО|ӘКЕСІНІҢ АТЫ|ИМЯ|АТЫ)")


def shape_name(text):
    return " ".join(unicodedata.normalize("NFC", text).upper().translate(str.maketrans(ALIASES)).split())


def select_name(text, score, reread, reread_score, minimum):
    if shape_name(reread) == shape_name(text):
        return reread, max(score, reread_score), False
    # Generic detection text is preliminary. A confident reading by the
    # field's script recognizer may replace an uncertain preliminary reading.
    # Contradictory confident readings always remain a manual-review case.
    if score >= minimum:
        return text, score, True
    return reread, reread_score, False


class NameDecoder:
    def __init__(self, base):
        self.base = base
        self.pairs = [(base.dict[a], base.dict[b]) for a, b in ALIASES.items() if a != b and a in base.dict and b in base.dict]

    def __call__(self, predictions, *args, **kwargs):
        import numpy as np
        probs = predictions.copy()
        if np.isfinite(probs).all() and probs.min() >= 0 and probs.max() <= 1 and np.allclose(probs.sum(axis=-1), 1, atol=0.005):
            for latin, cyrillic in self.pairs:
                probs[..., cyrillic] += probs[..., latin]
                probs[..., latin] = 0
            np.clip(probs, 0, 1, out=probs)
        return self.base(probs, *args, **kwargs)


def refine_names(engine, image, result):
    from rapidocr.ch_ppocr_rec import TextRecInput
    from rapidocr.utils.process_img import get_rotate_crop_image
    text = " ".join(result.txts or []).upper()
    is_id = ("УДОСТОВЕРЕНИЕ" in text and "ЛИЧНОСТИ" in text) or ("ЖЕКЕ" in text and "КУӘЛІК" in text)
    if not is_id or result.boxes is None:
        return {}
    indices = []
    for i, line in enumerate(result.txts or []):
        if NAME_LABEL.search(line.upper()) and i + 1 < len(result.txts):
            candidate = result.txts[i + 1]
            if not NAME_LABEL.search(candidate.upper()) and len(candidate) <= 60 and all(c.isalpha() or c in " '-." for c in candidate):
                indices.append(i + 1)
    rec = engine.text_rec
    original = rec.postprocess_op
    refined = {}
    try:
        rec.postprocess_op = NameDecoder(original)
        for i in indices[:3]:
            crop = get_rotate_crop_image(image, result.boxes[i].copy())
            reread = rec(TextRecInput(img=[crop], return_word_box=False))
            if reread.txts:
                refined[i] = (reread.txts[0], float(reread.scores[0]))
    finally:
        rec.postprocess_op = original
    return refined


def main():
    from rapidocr import RapidOCR, OCRVersion, ModelType
    import cv2

    cv2.setNumThreads(1)
    root = Path(os.environ.get("OCR_MODELS_DIR", "/opt/ocr-models"))
    models = {"Det": "ch_PP-OCRv5_det_mobile.onnx", "Cls": "ch_ppocr_mobile_v2.0_cls_mobile.onnx", "Rec": "cyrillic_PP-OCRv5_rec_mobile.onnx"}
    if any(not (root / name).is_file() for name in models.values()):
        raise RuntimeError("models_unavailable")
    params = {
        "Global.log_level": "error",
        "Global.max_side_len": 2020,
        "Global.return_word_box": True,
        "Det.ocr_version": OCRVersion.PPOCRV5,
        "Det.model_type": ModelType.MOBILE,
        "Rec.ocr_version": OCRVersion.PPOCRV5,
        "Rec.lang_type": "cyrillic",
        "Rec.model_type": ModelType.MOBILE,
        "Rec.rec_batch_num": 1,
        "Cls.cls_batch_num": 1,
        "EngineConfig.onnxruntime.intra_op_num_threads": 1,
        "EngineConfig.onnxruntime.inter_op_num_threads": 1,
    }
    for task, name in models.items():
        params[task + ".model_path"] = str(root / name)
    # Some dependency versions print startup notices. Keep the JSON channel clean.
    with contextlib.redirect_stdout(sys.stderr):
        engine = RapidOCR(params=params)
    if sys.argv[1:] == ["--check"]:
        print('{"ready":true}')
        return
    paths = sys.argv[1:]
    if not 1 <= len(paths) <= 8:
        raise RuntimeError("image_limit")
    pages = []
    minimum = float(os.environ.get("VERIFICATION_MIN_CONFIDENCE", "98")) / 100
    if not 0.90 <= minimum <= 1:
        raise RuntimeError("invalid_confidence")
    for path in paths:
        image = cv2.imread(path)
        if image is None:
            raise RuntimeError("image_unavailable")
        with contextlib.redirect_stdout(sys.stderr):
            result = engine(image)
            refinements = refine_names(engine, image, result)
        lines = []
        for i, (text, score) in enumerate(zip(result.txts or [], result.scores or [])):
            if len(lines) >= 1000 or len(text) > 1024:
                raise RuntimeError("output_limit")
            words = []
            if result.word_results and i < len(result.word_results):
                for item in result.word_results[i] or []:
                    if len(item) >= 2 and isinstance(item[0], str):
                        words.append({"Text": item[0], "Confidence": float(item[1]) * 100})
            # Use word offsets only when the adapter's word grouping exactly
            # reconstructs this line. Otherwise its native line score is safer.
            if " ".join(w["Text"] for w in words).split() != text.split():
                words = []
            conflict = False
            if i in refinements:
                reread, reread_score = refinements[i]
                text, score, conflict = select_name(text, float(score), reread, reread_score, minimum)
                words = []
            lines.append({"Text": text, "Confidence": float(score) * 100, "Words": words, "Conflict": conflict})
        pages.append(lines)
    print(json.dumps(pages, ensure_ascii=False, allow_nan=False))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Tracebacks from dependencies may contain file paths or private text.
        print("OCR adapter failed", file=sys.stderr)
        sys.exit(1)
