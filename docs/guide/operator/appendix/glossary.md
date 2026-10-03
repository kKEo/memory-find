# Glossary

| Term | Meaning |
|---|---|
| **Abstention** | Returning zero results on purpose, with a `reason`, when nothing passes the semantic floor |
| **Address** | A `memo://<kind>/<id>` URI identifying one record; it resolves even after the record is forgotten |
| **Arm** | One retrieval method producing a ranked list: keyword, exact, semantic, fact, entity, graph |
| **`as_of`** | A time-travel view: what the knowledge base had recorded, and believed valid, at a given time |
| **Audit** | The table recording every write: actor, channel, operation, target |
| **Band** | A relevance class (strong, moderate, weak) from the best raw cosine, using thresholds per model |
| **BM25** | The term-frequency ranking function FTS5 uses for the keyword and exact arms |
| **Channel** | How a write arrived: `tool`, `cli`, `elicitation`, `worker`. It determines trust |
| **Chunk / passage** | A section of a document of about 200 tokens: the unit of indexing and retrieval |
| **Compaction** | Agent-performed maintenance: writing pages, settling conflicts, deciding merges |
| **Cutoff** | Where a result list ends: a score gap, the limit, the token budget, or none |
| **Degraded** | A search served without a capability, usually the embedding model |
| **Elicitation** | The MCP mechanism by which a server asks the human a question through the client |
| **Embedding** | A vector representing a text's meaning; compared with cosine similarity |
| **Entity** | A named thing (system, person, identifier) extracted from passages into the graph layer |
| **Fact** | One-sentence claim with evidence, a subject list and a validity window |
| **FTS5** | SQLite's full-text search extension; memo-mcp keeps two indexes (stemmed, and identifier-preserving) |
| **Fusion** | Combining arm lists into one ranking: RRF (rank-based) or min-max (score-based) |
| **Granularity** | What a search returns: `chunk`, `document`, `fact` or `page` |
| **Knowledge base (KB)** | One SQLite file, selected by `MEMO_KB`; the isolation boundary |
| **MCP** | Model Context Protocol: the JSON-RPC protocol between AI clients and tool servers |
| **Merge candidate** | Two entity names similar enough to possibly be the same thing; queued for a human |
| **Namespace** | A label grouping sources inside one knowledge base; searches span all by default |
| **Origin** | Declared provenance class: `web`, `user-said`, `agent-derived` |
| **Page** | Agent-written markdown about an entity, stored as an inference with its cited passages |
| **PPR** | Personalised PageRank: the random walk the graph arm runs from each named entity |
| **Profile** | A named set of ranking constants (`MEMO_PROFILE`) |
| **Recency** | A multiplicative score factor that decays with age for kinds that age |
| **Revision** | One version of a source's document; changed content creates a new revision |
| **RRF** | Reciprocal rank fusion: sum of `weight / (k + rank)` over arms |
| **Source** | Where content came from (URI or file), with library, version and trust |
| **Stale** | A page whose sources changed or were forgotten since it was built |
| **Supersede** | Replace a revision or fact with a newer one, keeping the old one as history |
| **Trace** | The per-query explanation: arms run, candidates, latency, filters, cutoff, budget |
| **Trust** | Who vouched for a record: `agent` < `user` < `curated`; only humans raise it |
| **WAL** | SQLite's write-ahead log mode, which lets readers and a writer work concurrently |
| **Why** | The per-result explanation: each arm's rank and contribution, fused and final scores |
| **Work item** | One compaction task with everything needed to do it in its payload |
