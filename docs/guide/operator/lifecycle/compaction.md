# Compaction and pages

**The server never writes prose.** Compaction is a queue of work items that an agent, or a
person, completes. The server checks the result before storing it.

## Work items

`compact`, through the tool or `memo-mcp compact`, scans a namespace and creates items:

| Kind | Created when |
|---|---|
| `page` | An entity has 3 or more live passages and no page |
| `stale` | A page's sources changed or were forgotten, or the entity gained 2 or more passages since the page was built |
| `conflict` | Two live facts about one subject overlap in time and disagree |
| `merge` | An open merge candidate exists |
| `duplicate` | Two passages from different documents are near duplicates (word 3-gram Jaccard of at least 0.75) |

Each item's payload carries everything needed to do it: the passages, the facts and the previous
page. No second call is needed.

## Submitting

```bash
memo-mcp compact --ns default --kinds page,stale --json
memo-mcp submit 42 --content-file page.md --title "Quorum replication" --dry-run
memo-mcp submit 42 --content-file page.md --title "Quorum replication"
memo-mcp submit 43 --keep memo://fact/0199…
memo-mcp submit 44 --accept            # merge item
memo-mcp submit 45 --skip --reason "not worth a page"
```

Every page submission reports:

- **diff**: a line diff against the previous page.
- **omission check**: recorded facts about the subject whose content words are mostly absent
  from the page. Less than 75% coverage is flagged.
- **corruption check**: page sentences that share fewer than half of their content words with
  any source.

Pages are stored as `is_inference = 1`, with the passages they cite. They are searchable with
`granularity: page`, and marked `stale` when a source changes. Raw rows are never modified.

## Lint

```bash
memo-mcp lint --json
```

Reports contradictions, orphan entities, missing pages, stale pages and expired facts, with
addresses. Lint changes nothing.

## The optional Ollama executor

For unattended page writing from the terminal:

```bash
MEMO_OLLAMA_MODEL=qwen2.5:7b-instruct memo-mcp compact --kinds page --executor ollama          # dry run
MEMO_OLLAMA_MODEL=qwen2.5:7b-instruct memo-mcp compact --kinds page --executor ollama --apply  # submit
```

- The endpoint is `MEMO_OLLAMA_URL`, default `http://127.0.0.1:11434`. It must be loopback
  unless `--allow-remote` is passed.
- Use a non-thinking instruction model. Every page still goes through the same checks.
- No MCP code path can reach the executor.

## Observing it

`memo_kb_work_items_open`, `memo_kb_pages`, `memo_kb_pages_stale`,
`memo_store_work_items_total{kind,event}` and `memo_store_pages_marked_stale_total`.
