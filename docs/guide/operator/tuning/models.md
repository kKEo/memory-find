# Embedding models

The semantic arm needs an embedding model. Models run **in-process** through a pure-Go ONNX
backend (hugot / GoMLX), so no Python, no ONNX Runtime and no GPU are involved. `potion` is a
static lookup table with no neural network at all.

```bash
memors-mcp model ls
```

| Id | Dim | Licence | Size | Query latency* | Notes |
|---|---|---|---|---|---|
| `granite-small-r2` (default) | 384 | Apache-2.0 | ~195 MB | ~0.2 s | IBM granite-embedding-small-english-r2; best measured quality; 8k context |
| `minilm` | 384 | Apache-2.0 | ~87 MB | ~0.12 s | The previous default; no paraphrase recall in the bake-off |
| `potion` | 512 | MIT | ~130 MB | ~6 ms | model2vec table; instant; weaker on paraphrase; good fallback |
| `granite-r2` | 768 | Apache-2.0 | ~600 MB | slow | Runs, but about 12× slower than MiniLM on this backend |
| `arctic-m-v2` | 768 | Apache-2.0 | ~1.2 GB | slow | Snowflake; multilingual |
| `gemma-256` | 256 | Gemma Terms of Use | ~310 MB | slow | Opt-in only: not an Apache or MIT licence |

\* p50 query latency including query embedding, measured in `docs/eval/v0.7.0.md`. Writes cost
more: about 0.6 s per passage with `granite-small-r2` on this backend.

Each model has its own relevance bands in the registry. For `granite-small-r2` they are
0.88 / 0.80 / 0.72, because unrelated text already scores about 0.63 under it.

## How vectors are stored

Vectors are stored per passage **per model** in `chunk_vecs(chunk_id, model_id, embedding)`.
Several models can coexist in one file, so switching back is free once both are embedded.
Each 384-dimension vector adds about 1.5 KB per passage.

## Switching models

1. **Pre-download and check**, optionally:
   ```bash
   memors-mcp model pull potion
   memors-mcp model smoke potion
   ```
2. **Embed the existing passages.** This is optional, because the server does it in the
   background; doing it ahead avoids a degraded period:
   ```bash
   MEMORS_KB=my-project memors-mcp reindex --model potion
   ```
   `reindex` is resumable. It only embeds passages that lack a vector for that model.
3. **Record the default** for the file, and **set `MEMORS_MODEL`** in the MCP client's server
   entry:
   ```bash
   MEMORS_KB=my-project memors-mcp model use potion
   ```
4. Restart the client session. At start, the server runs `backfill`, then `reindex` for the
   current model, in the background. Until that finishes, search reports `degraded` and
   `memors_kb_pending_embeddings{model="potion"}` is above zero.

## Downloads

- Source: Hugging Face, over HTTPS, into `~/.cache/memors-mcp/models/`.
- A completed download writes a `.ok` marker. An interrupted one is detected and retried.
- `memors-mcp model redownload <id>` discards the cache for one model and fetches it again.
  Always pass the id: without one it re-downloads `minilm`.
- Metrics: `memors_embed_model_downloads_total{model,outcome}`,
  `memors_embed_model_load_seconds`, `memors_embed_model_loaded`.

Offline installs: [Air-gapped installs](../install/air-gapped.md).
