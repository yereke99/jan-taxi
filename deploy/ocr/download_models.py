"""Build-time model download from the RapidOCR release manifest; no user input."""
import hashlib
from pathlib import Path
import urllib.request

ROOT = Path("/opt/ocr-models")
BASE = "https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/"
MODELS = {
    "PP-OCRv5/det/ch_PP-OCRv5_det_mobile.onnx": "4d97c44a20d30a81aad087d6a396b08f786c4635742afc391f6621f5c6ae78ae",
    "PP-OCRv4/cls/ch_ppocr_mobile_v2.0_cls_mobile.onnx": "e47acedf663230f8863ff1ab0e64dd2d82b838fceb5957146dab185a89d6215c",
    "PP-OCRv5/rec/cyrillic_PP-OCRv5_rec_mobile.onnx": "90f761b4bfcce0c8c561c0cb5c887b0971d3ec01c32164bdf7374a35b0982711",
}
ROOT.mkdir(parents=True, exist_ok=True)
for name, expected in MODELS.items():
    with urllib.request.urlopen(BASE + name, timeout=60) as response:
        data = response.read(16 * 1024 * 1024 + 1)
    if len(data) > 16 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != expected:
        raise RuntimeError("model checksum mismatch")
    (ROOT / Path(name).name).write_bytes(data)
