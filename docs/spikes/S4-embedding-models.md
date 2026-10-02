# S4 — Which embedding models load under the pure-Go runtime? (P0 quick pass)

**Question.** hugot's pure-Go backend (GoMLX via onnx-gomlx) only runs ONNX graphs whose
operations it implements, so before the P3 bake-off we need to know which candidates load at all,
whether they embed sanely (`sim(a, a') > sim(a, b)`), their dimension, and their latency. P0 only
has to prove the harness works and learn the repository layouts; the full pass with every
candidate and the rerankers runs in P3.

**Run.** 2026-10-02, `go run -tags spike ./spikes/s4-models <repo>[@<onnx path>]`, one model per
process with a 20-minute watchdog, Apple Silicon laptop, hugot v0.7.2. Text for timing: about 250
word-pieces; batch of 16 identical texts.

**Numbers.**

| Model | Loads | Sane | Dim | p50, 1 × ~256 tokens | p50, batch 16 | Note |
|---|---|---|---|---|---|---|
| sentence-transformers/all-MiniLM-L6-v2 (control) | yes | yes (sim(a,a′) = 0.92, sim(a,b) = 0.04) | 384 | **884 ms** | **10.3 s** | the only candidate that ran |
| Snowflake/snowflake-arctic-embed-m-v2.0 (`onnx/model.onnx`) | no | – | – | – | – | "failed to parse ONNX model proto: invalid wire-format data": the downloaded file is not a readable graph (possibly a Git-LFS pointer or a layout the downloader mishandles); needs a look at what landed on disk |
| onnx-community/granite-embedding-small-english-r2-ONNX | no | – | – | – | – | weights are **external data** (`model.onnx_data`); the spike did not set `DownloadOptions.ExternalDataPath`, so the file was never fetched. Harness gap, not an op-coverage verdict |
| onnx-community/granite-embedding-english-r2-ONNX | no | – | – | – | – | same external-data gap |
| onnx-community/embeddinggemma-300m-ONNX (`onnx/model_quantized.onnx`) | no | – | – | – | – | same external-data gap |

Two other observations from running the harness:

- **The downloader can hang.** The first run deadlocked inside go-huggingface's download semaphore
  (`all goroutines are asleep`) after a previous partial download was left in the target directory.
  Clearing the directory fixed it. This is the failure the roadmap's download-hardening items
  (sweep stale temp dirs, reachable lock timeout, lazy start) exist for; it also argues for a
  download timeout in P3's registry.
- **Repositories need the ONNX path spelled out.** Repos that ship several `.onnx` variants
  (`model.onnx`, `model_fp16.onnx`, `model_int8.onnx`, …) make `hugot.DownloadModel` refuse
  unless `OnnxFilePath` is set; production already sets `onnx/model.onnx` for MiniLM. Repos whose
  weights live in a separate `.onnx_data` file also need `ExternalDataPath`.

**Decision.**

1. The MiniLM control number is the headline: **about 0.9 s per ~256-token text and 10 s per
   batch of 16 on this laptop**, roughly 10× slower than ONNX Runtime as the research document
   warned. Any larger model must be measured before it is promised; a 300M-parameter model at
   this ratio would be several seconds per text. The P3 bake-off therefore keeps latency as a hard
   column, keeps MiniLM as the incumbent, and keeps the static `potion` model as the instant tier.
2. The P3 full pass must set `ExternalDataPath` for the onnx-community exports, inspect what the
   arctic download produced, and add the quantised variants and the rerankers. Only after that
   can any candidate be dropped for op coverage.
3. The `models` table (P1) records the exact `hf_repo`, the ONNX path inside it and the external
   data path, since all three turned out to be needed to load a model at all.
