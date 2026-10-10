# Ingest and revisions

## The write path

![Normalise and hash; if the hash and version match the current revision, nothing is written. Otherwise one SQLite transaction writes the new revision, the chunks, both FTS5 indexes and entity mentions, and the audit row. After the commit the passages are embedded in batches of 16, or queued as a job when no model is available.](../images/write-path.svg)

1. **Normalise** line endings and whitespace, and hash the content (SHA-256).
2. **Deduplicate.** If the source's current revision has the same hash, nothing happens and the
   result says `dedup: true`. If the content differs, a new revision is created. The previous
   revision is marked superseded and stays readable with `as_of`.
3. **Chunk.** The text is split by headings, then paragraphs, then sentences, then a hard
   split. The target is about 200 estimated tokens and the cap 400, clamped to the model's
   limit, with about 40 tokens of overlap. Fenced code blocks are never split. Each passage gets
   a context header `title > section path` that the keyword index also sees.
4. **Index.** Both FTS5 indexes are filled by triggers inside the transaction. Entity mentions
   are extracted and linked.
5. **Audit**: one row per write, in the same transaction.
6. **Embed** outside the transaction, in batches of 16. If no model is available, the
   passages are queued as a job, and `backfill` or the next server start embeds them. A write
   never reports success it did not get.

A **source** is identified by its URI, or by its file path for CLI ingests. Re-ingesting the
same URI with changed content, or with a new `version`, creates a new revision of that source.

## From the CLI

```bash
memo-mcp ingest ./docs --ns handbook --kind doc
memo-mcp ingest page.md --uri https://pkg.go.dev/google.golang.org/grpc \
  --library grpc/grpc-go --version v1.64.0 --context "gRPC-Go API docs for deadlines"
cat notes.md | memo-mcp ingest - --kind note --title "Standup 2026-10-03"
memo-mcp ingest ./big-folder --embed=false && memo-mcp backfill   # fast load, embed later
```

CLI writes are trust `user`, or `curated` with `--trust curated`. Origin defaults to `web`
when `--uri` is set, and to `user-said` otherwise.

## Kinds

| Kind | Ages under recency? | Typical content |
|---|---|---|
| `doc` | No | Documentation, specs, handbooks; usually versioned |
| `code` | No | Code explanations and snippets |
| `note` | Yes | Short notes, decisions, observations |
| `conversation` | Yes | Conversation summaries |

## Costs

| Step | Cost |
|---|---|
| Parse, chunk, index | About 60 documents per second without embedding |
| Embedding with `granite-small-r2` | About 0.6 s per passage on the pure-Go backend |
| Embedding with `potion` | Near instant |
| Disk | About 4.4 KB per passage before vectors, plus about 1.5 KB per passage per 384-dimension model |

For bulk loads, ingest with `--embed=false`, then run `memo-mcp backfill`, or let the server
embed in the background. See [Capacity and performance](../operations/capacity.md).

## Observing it

`memo_store_ingests_total{outcome=new|revision|dedup|error}`,
`memo_store_ingest_duration_seconds`, `memo_store_chunks_written_total`,
`memo_store_embed_batches_total{outcome}`, and `memo_kb_pending_embeddings{model}` for the
backlog.
