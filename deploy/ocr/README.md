# Offline document recognition

The Go worker runs this adapter as a bounded child process, once per job. It uses
RapidOCR 3.10.0 / ONNX Runtime with PP-OCRv5 mobile detection and Cyrillic
recognition, plus the PP-OCR mobile orientation classifier. Original documents
stay in private storage. The adapter does not call a remote recognition API.

Dependencies are pinned in `requirements.txt`. `download_models.py` downloads
three models from the versioned RapidAI manifest at image build time and checks
their SHA-256 hashes. Runtime model paths are explicit; downloads are not needed
when a driver submits documents. The images are processed sequentially with one
CPU thread and batch size one.

Names on an ID card receive a second recognition pass in their labelled field.
Its decoder combines probability mass for identical Latin/Cyrillic glyphs only;
distinct Kazakh letters remain distinct. It does not see registration inputs.
Conflicting confident readings require review. Field scores still have to meet
the configured threshold, and the Go service applies identity comparisons and
all existing approval guards.

Upstream projects and attribution:

- RapidOCR: Copyright (c) 2021 RapidOCR Authors; Apache-2.0.
  <https://github.com/RapidAI/RapidOCR/tree/v3.10.0>
- PaddleOCR models: <https://github.com/PaddlePaddle/PaddleOCR>
  and <https://rapidai.github.io/RapidOCRDocs/main/model_list/>.
- The upstream Apache license is included in `LICENSE.upstream`; installed
  Python distributions retain their own license metadata inside the image.

The adapter is local integration code, not an unmodified upstream script.
`test_recognize.py` tests decoder policy without private documents. The Go
integration test generates synthetic labelled panels, then checks a combined
PNG and a combined PDF uploaded in both slots against the actual recognizer.
