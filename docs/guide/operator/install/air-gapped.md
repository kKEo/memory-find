# Air-gapped installs

After the binary is in place, the only network access memors-mcp ever makes is downloading an
embedding model the first time it is needed. To run with no network at all, pre-seed the
model cache.

## Pre-seed the model cache

On a connected machine with the same memors-mcp version:

```bash
memors-mcp model pull granite-small-r2     # or the model you will set in MEMORS_MODEL
ls ~/.cache/memors-mcp/models/
#   onnx-community_granite-embedding-small-english-r2-ONNX/
#   onnx-community_granite-embedding-small-english-r2-ONNX.ok
```

Copy **both** the directory and its `.ok` marker to the same path on the target machine,
`~/.cache/memors-mcp/models/`, for the user who runs memors-mcp. The marker records a completed,
verified download. Without it, the files are treated as an interrupted download and fetched
again.

Then check that the model loads offline:

```bash
memors-mcp model smoke granite-small-r2
```

For the optional reranker, also copy `cross-encoder_ms-marco-MiniLM-L6-v2` and its marker.

## Choosing a smaller model

| Model id | Download | Notes |
|---|---|---|
| `granite-small-r2` (default) | about 195 MB | Best measured quality; about 0.2 s per query on this backend |
| `potion` | about 130 MB | Static lookup table, no neural network; about 6 ms per query; weaker on paraphrase |
| `minilm` | about 87 MB | The previous default |

See [Embedding models](../tuning/models.md) for the full registry.

## Running with no model at all

If no model is cached and the download fails, memors-mcp keeps working. Search runs on the
keyword, exact and fact arms and reports `degraded`. New passages queue their vectors. When a
model becomes available, `memors-mcp backfill` or the next server start embeds the backlog.
