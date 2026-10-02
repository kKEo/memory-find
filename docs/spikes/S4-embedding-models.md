# S4 — Which embedding models load under the pure-Go runtime?

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

---

## Full pass (P3, 2026-10-02, `memo-mcp model smoke --all`)

The harness gaps above are fixed: the registry records each repo's ONNX path and external
weights file, static models are read directly from safetensors, and `model smoke` is a command.
Same laptop (Apple Silicon), hugot v0.7.2 pure-Go backend, one process per model.

| Model | Loads (ms) | Sane | Dim | p50, 1 × ~256 tokens | p50, batch 16 | Licence | Verdict |
|---|---|---|---|---|---|---|---|
| minilm (all-MiniLM-L6-v2) | 168 | yes (0.92 vs 0.04) | 384 | **593 ms** | 9.8 s | Apache-2.0 | incumbent; the latency floor for ONNX models |
| potion (potion-retrieval-32M, static) | 21,036 (reads a 129 MB table) | yes (0.84 vs −0.07) | 512 | **0 ms** | 2 ms | MIT | the instant tier: no network pass at all |
| granite-small-r2 (onnx-community, fp32) | 31,924 | yes (0.94 vs 0.63) | 384 | 1,956 ms | 36 s | Apache-2.0 | runs; 3.3× slower than MiniLM; quality decided by eval |
| granite-r2 (onnx-community, fp32) | 117,572 | yes (0.95 vs 0.47) | 768 | 7,173 ms | 140 s | Apache-2.0 | runs; 12× slower than MiniLM; too slow for queries |
| gemma-256 (embeddinggemma q8) | loads | **no** | – | – | – | Gemma | op coverage: `DequantizeLinear` with a per-tensor scale is not implemented by onnx-gomlx; the fp32 export (1.2 GB) was not tried |
| arctic-m-v2 (fp32 `onnx/model.onnx`) | **no** | – | – | – | – | Apache-2.0 | the 1.2 GB graph does not parse ("invalid wire-format data"); not a GoMLX op issue but a file the pure-Go parser cannot read |

**What the numbers mean for OD-6.** The OD-6 rule said "p50 ≤ 150 ms for 256 tokens"; no ONNX
model meets it under the pure-Go backend, including the incumbent (593 ms). The rule has to be
relative: *no slower than 2× MiniLM, and better on eval nDCG@10 by at least 0.02*. Two models
survive the smoke as realistic candidates: granite-small-r2 (same 384-d, Apache-2.0, 3.3× slower,
so it fails the latency half unless its quality gain is large) and potion (instant, MIT, a static
model whose paraphrase quality the eval must show). The decision is made in
`docs/eval/v0.7.0.md` from the measured eval, not here.

**Harness notes.** The static loader reads safetensors F32/F16 and a WordPiece `tokenizer.json`
(BERT pre-tokenisation, greedy longest match). The quantised onnx-community exports all share the
`DequantizeLinear` gap, so quantisation is not a path to speed on this backend today. Load times
are dominated by reading weights into GoMLX; a long-running server pays them once.
