# memo-mcp as a Domain Knowledge Base — State of the Art & Target Architecture

> **Renamed.** This document predates the rename to memors (2026-10-10): `memo-mcp` is now `memors-mcp`, `memo-tray` is `memors-tray`, `MEMO_*` is `MEMORS_*` and `~/.memo-mcp` is `~/.memors-mcp`. `memo://` addresses are unchanged.

*Research synthesis, 2026-10-01. Supersedes the design premises of
[`history/graphrag-evolution-plan.md`](history/graphrag-evolution-plan.md) and
extends the 2026-09-04 review (Phases 0–1 largely shipped on 2026-09-04,
untagged; the leftovers are listed in the roadmap). The live plan that turns
this research into phases is [`roadmap.md`](roadmap.md).*

## 0. The goal, restated

memo-mcp should become a **domain knowledge base**:

1. **Populated on demand.** Knowledge goes in when an agent or user needs it,
   e.g. "learn library X v3", "ingest these docs", "remember this finding".
   Nothing crawls silently in the background.
2. **Compacted over time.** Raw material is consolidated into higher-level
   structure: facts, entities, relations, topic and entity pages. Together these
   form a knowledge graph.
3. **Flexibly accessed.** The caller picks strategy, scope, granularity, time
   point, and token budget *on each request*. These are not deployment-time
   constants.
4. **Serving two kinds of consumer.** Conversational chatbots want natural
   language, citations, and summaries. Coding agents want precise,
   version-pinned, token-frugal content that they can dereference and grep.

The research below covers four tracks: agent memory systems, graph RAG and
compaction, retrieval and request-time access, and on-demand ingestion with MCP
integration. Each section ends with the decision it drives. §6 assembles the
decisions into a target architecture, and §7 maps that architecture onto the
existing roadmap.

> **Verification caveat.** Every number below comes from a primary source found
> during research. Many are **vendor-reported**, and memory-system benchmarks
> in particular are disputed (§1.3). Items that could not be verified are
> listed in §9. Re-check them before citing externally or building on them.

---

## 1. Agent memory systems: what the field has learned

### 1.1 Landscape

| System | Write path | Update / conflict model | Temporal | Local? |
|---|---|---|---|---|
| **Mem0** (paper [2504.19413](https://arxiv.org/abs/2504.19413), Apr 2025; **v3 algorithm, Apr 2026**) | v2: LLM extraction, then **ADD / UPDATE / DELETE / NOOP**. **v3: single-pass, ADD-only** ([migration](https://docs.mem0.ai/migration/oss-v2-to-v3)) | v3 keeps both old and new facts; the app decides which is current (timestamps, metadata). **Graph memory removed from OSS** and replaced by entity linking in a `{collection}_entities` store | Time-aware ranking | Yes (Ollama); Apache-2.0; OpenMemory MCP |
| **Zep / Graphiti** ([2501.13956](https://arxiv.org/abs/2501.13956), Jan 2025) | Episodes → LLM entity and edge extraction → entity resolution (embedding + full-text + LLM) | **Invalidate, never delete** | **Bi-temporal**: `valid_at`/`invalid_at` + `created_at`/`expired_at` | Graphiti OSS (Neo4j/FalkorDB) |
| **Letta / MemGPT** ([2310.08560](https://arxiv.org/abs/2310.08560); sleep-time agents Apr 2025) | The agent edits its own memory blocks via tools | Agent rewrites; a sleep-time agent consolidates asynchronously | Implicit | Yes; Apache-2.0 |
| **Cognee** (2025–26) | Extract → "cognify" (LLM KG + summaries) → load; `memify` enrichment | Re-cognify; ontology grounding | Temporal mode | Yes; Apache-2.0 |
| **A-MEM** ([2502.12110](https://arxiv.org/abs/2502.12110), NeurIPS 2025) | Zettelkasten notes (content, keywords, tags, context) | **Memory evolution**: a new note updates the metadata of linked old notes | None | Research code |
| **MIRIX** ([2507.07957](https://arxiv.org/abs/2507.07957)) | Six typed stores (core, episodic, semantic, procedural, resource, vault), each with its own agent | Per-type managers | Episodic | Apache-2.0 |
| **MemOS** ([2505.22101](https://arxiv.org/abs/2505.22101)) | "MemCube" unifies text, KV-cache, and LoRA memory | Lifecycle scheduler, versioning | Versioned | Apache-2.0 |
| **Anthropic memory tool** (beta Sep 2025) | **No extraction**: the model edits files under `/memories` | The model edits | None | Client-hosted |
| **Claude Code Auto Memory** (v2.1.59, Feb 2026) | Claude writes `MEMORY.md` + topic files | The model edits | None | Local markdown |
| **basic-memory** (2025) | Agent writes markdown notes with `- [category] fact` / `- relation [[Target]]` markup | File edits + SQLite index | None | AGPL-3.0, local |
| **MCP reference `memory` server** | `create_entities`, `create_relations`, `add_observations` | Manual | None | MIT, deliberately toy (whole-graph JSONL) |

Three write-path families:

1. **Extract-and-consolidate** (Mem0 ≤ v2, Graphiti, Cognee, LangMem). Compact
   and precise, but costs 1–3 LLM calls per write and is **lossy**: anything not
   judged salient at write time is gone. Mem0 left this family for ADD-only
   extraction in v3 (§1.5).
2. **Agent-edits-memory** (Letta, the Anthropic memory tool, Auto Memory,
   basic-memory). Transparent and auditable, but quality depends on the model's
   diligence.
3. **Store raw, then index** (classic RAG; memo-mcp today). Lossless and needs no
   LLM at write time. It is weaker at knowledge-update and aggregation
   questions.

### 1.2 Benchmarks

- **LoCoMo** ([2402.17753](https://arxiv.org/abs/2402.17753)): roughly 10
  conversations of about 9k tokens each. That **fits in a modern context
  window**, so the benchmark is effectively saturated.
- **LongMemEval** ([2410.10813](https://arxiv.org/abs/2410.10813), ICLR 2025): 500
  questions covering five abilities: **information extraction, multi-session
  reasoning, temporal reasoning, knowledge update, abstention**. Long-context
  LLMs lose 30–60% against oracle. The paper's recommended design is to
  **keep raw sessions as values and expand the keys with extracted facts**, plus
  time-aware query expansion.
- **MemoryAgentBench** ([2507.05257](https://arxiv.org/abs/2507.05257), Jul 2025):
  measures accurate retrieval, test-time learning, long-range understanding, and
  **conflict resolution**. Conflict resolution is broadly poor; multi-hop
  conflict accuracy is often below 10%.
- **BEAM** ([2510.27246](https://arxiv.org/abs/2510.27246), Oct 2025): up to 10M
  tokens and 10 abilities, including contradiction resolution and event
  ordering.

### 1.3 The numbers controversy, and the honest reading

The disputes are public:

- Mem0 claimed it beat Zep and OpenAI memory on LoCoMo.
- Zep published a rebuttal alleging misconfiguration.
- Letta ([Aug 2025](https://www.letta.com/blog/benchmarking-ai-agent-memory))
  then showed that **a plain agent with filesystem tools (grep over raw
  conversation files) scored about 74% on LoCoMo**. That is above Mem0's best
  reported result (about 68.5%).
- Mem0's own paper reports a **full-context baseline (about 72.9%) above Mem0
  (about 66.9%)**. The trade-off is much higher token usage and latency.

**Reading:** extracted-fact memory wins on **cost and latency**. It also wins on
**knowledge-update and temporal** questions, but only when time is modelled
explicitly. It does **not** win on raw accuracy. Raw chunks plus a capable
agent iterating over search tools is a strong baseline that is often better.

### 1.4 Offline consolidation ("sleep-time compute")

- **Generative Agents** (Park et al., 2023): memories are scored by recency ×
  importance × relevance. A *reflection* pass synthesises higher-level insights
  and stores them as memories that **cite their sources**.
- **Sleep-time compute** ([2504.13160](https://arxiv.org/abs/2504.13160), Apr
  2025): inferences are precomputed over stored context while the system is
  idle. This gave roughly a 5× reduction in test-time compute at equal accuracy,
  amortised across queries.

### 1.5 2026 developments (follow-up research)

- **The field is moving away from destructive updates.**
  - **Mem0 v3** (Apr 2026) dropped its own ADD/UPDATE/DELETE/NOOP write path
    for ADD-only extraction and deleted roughly 4k lines of graph-store code
    from OSS. Its reasoning was that overwriting throws information away.
  - Graphiti treats episodes as the ground-truth stream.
  - Supermemory separates documents from memories and keeps `isLatest`
    version chains.
  - MemMachine ([2604.04853](https://arxiv.org/abs/2604.04853)) is explicitly
    "ground-truth-preserving".
  - This all confirms D-A and D-B.
- **The embedder can matter more than the memory design: MemDelta.**
  [MemDelta](https://arxiv.org/abs/2606.29914) (Jun 2026, LongMemEval-S):
  - With **MiniLM** embeddings, Mem0 beats verbatim RAG 72.7% vs 61.4%.
  - **Changing only the embedder** makes RAG reach 73.9%, so Mem0 then
    *loses* by 1.2 pp, at roughly 50× the write cost.
  - An embedder swap alone moved accuracy 6.2 pp (p = 0.004).
  - Agent self-edited memory scored 42%, against 47% for basic RAG.
  - **memo-mcp runs MiniLM today, so this is the most directly applicable
    result in the literature.**
- **Retrieval-stage tuning beats ingestion-stage tuning.**
  - MemMachine's ablation: retrieval depth +4.2, formatting +2.0, search
    prompt +1.8, chunking only +0.8.
  - WhenLoss ([2605.24579](https://arxiv.org/abs/2605.24579)): in 4 of 6
    systems, accuracy is lost mainly **at write time**, when extraction or
    compression discards evidence.
  - On MemoryAgentBench accurate-retrieval tasks, plain **BM25 (60.5) beat
    Mem0 (32.6) and Cognee (28.3)**. Caveat: those systems ran with 4k-token
    chunks because of cost.
- **Offline consolidation ("dreaming") is now a product feature.** Examples
  include Letta Context Repositories with reflection and defrag (Feb 2026),
  Claude Managed Agents "dreaming" (research preview, May 2026), and ChatGPT
  "Dreaming V3" (Jun 2026); details for the last two are partly from press
  coverage. Common safety rails:
  - raw transcripts are never modified;
  - every change is versioned;
  - optional human review;
  - derived items are flagged as inferences.
- **Consolidation reliability is the 2026 research frontier.**
  - TRUSTMEM ([2606.25161](https://arxiv.org/abs/2606.25161)): omission is the
    dominant error, ahead of corruption and hallucination.
  - SSGM ([2603.11768](https://arxiv.org/abs/2603.11768)): drift compounds with
    each re-encoding (raw → summary → reflection), so validate before
    consolidating.
  - RecMem ([2605.16045](https://arxiv.org/abs/2605.16045)): extraction runs
    only when similar items *recur*, giving up to 87% lower build cost with
    better accuracy.
- **Newer benchmarks.**
  - **LongMemEval-V2** ([2605.12493](https://arxiv.org/abs/2605.12493), May
    2026): 25–115M-token web-agent trajectories, testing workflow knowledge
    and environment gotchas. The best system scores 72.5%; a
    **coding-agent-over-files baseline scores 69.3%**.
  - **Veracium** ([2607.21962](https://arxiv.org/abs/2607.21962), Jul 2026):
    facts with validity intervals are planted first, and conversations are
    generated from them. Rankings flip as history grows: full context wins
    short horizons, structured memory wins long ones.
- **Benchmark numbers are even less comparable than §1.3 suggests.**
  - An audit (Maximem, May 2026, itself a vendor) measured Mem0 at 57.5→73.8%
    on LongMemEval, against a claimed 93.4%. It attributes the gap to the
    judge setup.
  - The judge prompt alone can swap which system ranks first
    ([2602.19320](https://arxiv.org/abs/2602.19320)).

**Decision drivers**

- **D-A (keep raw).** Stay *store-raw-then-index* as the foundation. Derived
  knowledge is layered on top of it and never replaces it. Every derived record
  links back to raw chunks.
- **D-B (time).** Use the bi-temporal model and invalidate rather than delete.
  This is the one write-path idea with consensus behind it.
- **D-C (no server LLM).** The calling agent *is* an LLM. Let it supply
  extracted facts and entities as optional tool arguments: agent-edits-memory
  semantics at zero cost to the server.
- **D-D (consolidation).** Run consolidation as an explicit, asynchronous
  operation whose outputs are distinguishable from raw material.

---

## 2. Graph RAG and knowledge compaction

### 2.1 Two families

| Family | Systems | Indexing cost | Incremental? | Wins on |
|---|---|---|---|---|
| **Summary-heavy**: precompute LLM abstractions | MS GraphRAG ([2404.16130](https://arxiv.org/abs/2404.16130)), RAPTOR ([2401.18059](https://arxiv.org/abs/2401.18059)), ArchRAG, KAG | Very high: an LLM call for every chunk and every community | Poor: community and tree re-summarisation is effectively global | Global "sensemaking" questions |
| **Graph-as-index**: cheap structure, compute at query time | HippoRAG 2 ([2502.14802](https://arxiv.org/abs/2502.14802), ICML 2025), LightRAG ([2410.05779](https://arxiv.org/abs/2410.05779)), [LazyGraphRAG](https://www.microsoft.com/en-us/research/blog/lazygraphrag-setting-a-new-standard-for-quality-and-cost/) (Nov 2024), NodeRAG ([2504.11544](https://arxiv.org/abs/2504.11544)), MiniRAG ([2501.06713](https://arxiv.org/abs/2501.06713)) | Low to moderate. LazyGraphRAG indexing is **0.1% of GraphRAG's** because it uses NLP noun-phrase co-occurrence and no LLM | **Yes**: designed for unions and inserts | Multi-hop and local questions, with sensemaking via budgeted query-time deepening |

Notable details:

- **HippoRAG 2** runs **Personalized PageRank (PPR)** seeded from query-matched
  entities and passages over a graph built from OpenIE triples, passages, and
  synonym edges. It improves multi-hop QA *without losing on simple factual QA*,
  which is rare in this field.
- **MiniRAG** shows that LightRAG-style extraction **breaks with small local
  models**. That is relevant if extraction ever runs on a local small model.
- **Relation-free graphs work.** KET-RAG ([2502.09304](https://arxiv.org/abs/2502.09304),
  KDD 2025) uses a text–keyword bipartite graph over all chunks. LinearRAG
  ([2510.10114](https://arxiv.org/abs/2510.10114)) uses an entity "Tri-Graph" with
  **$0 LLM build cost**. E²GraphRAG ([2505.24226](https://arxiv.org/abs/2505.24226))
  uses SpaCy entities. All three recover most of the multi-hop gain using only
  **entity↔chunk links** and no typed relations.
- **2026 state of play:**
  - **MS GraphRAG is in maintenance mode.** Its README says it is "not
    accepting new features."
  - **LazyGraphRAG's query engine was never open-sourced.** Only the NLP
    extraction shipped.
  - **RAGFlow 0.27** (Aug 2026) **deprecated GraphRAG and RAPTOR** in favour of
    batch-built "Knowledge Compilation" artifacts: Wiki, Graph, Tree, Timeline
    ([release notes](https://ragflow.io/docs/v0.27.0/release_notes)).
  - The momentum is with cheap, query-time designs: HippoRAG 2, LinearRAG, and
    LiteRAG ([2609.10239](https://arxiv.org/abs/2609.10239), which uses hub
    penalisation).

### 2.2 When graphs help: independent evaluations

- **RAG vs. GraphRAG** ([2502.11371](https://arxiv.org/abs/2502.11371), Feb 2025):
  - On single-hop QA, vanilla RAG is as good or better.
  - Graph helps on multi-hop and summarisation questions.
  - Community-based global search *underperforms* on detail questions.
  - The paper recommends **routing between them**.
- **GraphRAG-Bench** ([2506.05690](https://arxiv.org/abs/2506.05690), Jun 2025):
  - Graph gains grow with task complexity, but many methods cost dramatically
    more for marginal gains.
  - **Extraction noise dominates downstream quality.**
  - GPT-4o-mini numbers from its ICLR'26 version:
    - **Fact retrieval:** RAG + rerank 60.9, HippoRAG 2 60.1, MS-GraphRAG 49.3.
    - **Complex reasoning:** HippoRAG 2 53.4, RAG 42.9.
  - **Prompt tokens per query:** vanilla RAG 879, **HippoRAG 2 1,008**, LightRAG
    about 100k, MS-global about 331k.
  - So the query-time "graph tax" is a design choice. **PPR re-ranking costs
    about +15%; community and summary context costs 100×+.**
- **GraphRAG-Bench on CS textbooks** ([2506.02404](https://arxiv.org/abs/2506.02404)):
  **BM25 alone scored 71.7**, against 72.5 for MS-GraphRAG and 73.6 for RAPTOR.
- **RAGSearch** ([2604.09666](https://arxiv.org/abs/2604.09666), Apr 2026):
  - In single-shot retrieval, the graph adds **+0.5 points on general QA but
    +27 points on multi-hop**.
  - **Agentic multi-turn search closes much of that gap**, and closes more of it
    as models get larger.
  - This supports §3.4: the calling agent is the router.
- LLM-judge "win-rate" evaluations, which the GraphRAG and LightRAG papers use,
  are **biased by answer length and position**.

### 2.3 Building the graph

- **Extract freely, then canonicalise.** Schema-free OpenIE maximises recall but
  produces relation explosion (`works_at` / `employed_by` / …). Two
  canonicalisation approaches:
  - **EDC** ([2404.03868](https://arxiv.org/abs/2404.03868)) has the LLM write a
    definition for each relation, embeds the definitions, and maps them onto the
    existing schema.
  - **AutoSchemaKG** ([2505.23628](https://arxiv.org/abs/2505.23628)) induces the
    schema at scale.
- **Entity resolution** in production systems (Graphiti, Cognee, Neo4j builder)
  works in three steps:
  1. Generate candidates by embedding similarity, fuzzy name match, and full-text
     search.
  2. An LLM merges ambiguous cases.
  3. Keep the canonical name plus aliases.

  A cheaper version is normalisation + an embedding threshold + neighbour
  overlap, with the LLM used only for the ambiguous band.
  - **Graphiti's current pipeline** (v0.21+, late 2025) is fully deterministic
    up to the last step, so it can be ported to pure Go:
    1. Exact match on the normalised name.
    2. 3-gram **MinHash + LSH** candidates, accepted at Jaccard ≥ 0.9.
    3. An **entropy gate** sends short or low-entropy names to an LLM.
    4. A second pass catches duplicates within the batch.
  - DEG-RAG ([2510.14271](https://arxiv.org/abs/2510.14271)) shows that merging
    duplicates and pruning bad triples shrinks graphs and *improves* QA.
- **Local extractors:**
  - **GLiNER / GLiNER2** ([2507.18546](https://arxiv.org/abs/2507.18546)):
    zero-shot NER, CPU-fast, ONNX available. There is **no Go port**.
    Tokenisation, span building, and decoding would have to be written by hand,
    and whether hugot/GoMLX supports DeBERTa ops is unverified. Prototype it
    before committing.
  - **GLiREL**: relation extraction.
  - **Triplex**: a Phi-3 fine-tune (self-reported numbers). **License is
    CC-BY-NC-SA, so commercial use is excluded.**
  - Small models below about 7B degrade noticeably on relation extraction.
  - Frugal KG ([2604.11104](https://arxiv.org/abs/2604.11104)) found that
    **thinking modes break JSON output**: 80% of Qwen3-14B outputs could not be
    parsed. Ollama extraction should use non-thinking models with constrained
    JSON.
- **No-LLM baseline:** LazyGraphRAG demonstrates that noun-phrase co-occurrence
  captures most of the structural value.

### 2.4 Compaction patterns, ranked by incremental friendliness

1. **Fact dedup / supersession** (Graphiti-style invalidation with validity
   intervals; Mem0 v3 went further and only adds). Cheap per write.
2. **Entity pages** (Graphiti node summaries). Only the entities a write touches
   get re-summarised, so this is incremental by construction.
3. **Topic communities.** Louvain or Leiden on the entity graph.
   - Zep uses label propagation: a new node joins the plurality community of
     its neighbours. Periodic full refreshes are still needed.
   - HIT-Leiden ([2601.08554](https://arxiv.org/abs/2601.08554)) is the
     principled incremental version.
4. **Living docs / "LLM wiki."** An agent maintains curated markdown pages, one
   per entity or topic, with backlinks to raw sources. Examples: DeepWiki,
   basic-memory, CLAUDE.md, Agent Skills. Pages are greppable, diffable, and
   cheap to serve. The risk is drift when provenance isn't kept.
   - Karpathy's "LLM wiki" pattern (Apr 2026) has three operations: ingest,
     query, and **lint**. Lint looks for contradictions, orphans, missing
     pages, and stale claims.
   - RAGFlow's Wiki artifact is the productised version.
5. **Hierarchical summary trees** (RAPTOR, GraphRAG community reports). Highest
   quality for global questions; worst for incremental updates.

### 2.5 Storage

- **Kuzu was archived in Oct 2025** and needs CGo. Do not adopt it.
- DuckDB/DuckPGQ needs CGo and only works on DuckDB 1.4.x.
- CozoDB needs CGo and has had no commits since Dec 2024.
- FalkorDBLite is Python-only and runs a Redis subprocess.
- EliasDB and Cayley are small or dormant projects.
- **Pragmatic pure-Go stack:**
  - Adjacency tables in SQLite are the source of truth.
  - A lazily built in-memory **CSR adjacency** per namespace serves PPR, which
    is a roughly 20-line power iteration, and gonum Louvain.
  - Recursive CTEs have no global visited set, so they blow up on hubs: a
    2-hop query from the top hub of a 100K-node power-law graph took about
    300 ms. Use unrolled fixed-depth joins with **hub-degree caps** instead.

**Decision drivers**

- **D-E (graph-as-index).** Use the graph-as-index family. Compute cheap
  structure eagerly, run PPR and budgeted expansion at query time, and produce
  summaries only through explicit compaction.
- **D-F (routed strategy).** The graph is **one routed retrieval strategy**
  (a third RRF arm), not the default path.
- **D-G (extraction ladder).** Each rung is a selectable, benchmarked strategy:
  1. Heuristic extraction: noun phrases and code identifiers.
  2. Client-supplied entities and relations.
  3. Optional GLiNER through ONNX.
  4. Optional local Ollama.

  None is mandatory.
- **D-H (old plan).** Drop the old plan's entry↔entry co-occurrence edges and
  "temporal precedes" edges. They are noisy and nothing in the literature
  supports them. **Relation-free entity↔chunk (mention) edges *are* well
  supported** (LazyGraphRAG, KET-RAG, LinearRAG, MiniRAG). They are the first
  rung of the graph. Typed entity↔entity edges come later and are optional.
- **D-H2 (entity resolution).** Run deterministic resolution at write time:
  normalise, then MinHash/LSH, then an entropy gate. Ambiguous pairs go to a
  `merge_candidates` queue that `compact` hands to the client LLM. Merges are
  never destructive: keep a canonical id plus aliases. Merge precision is
  measured in the eval harness.

---

## 3. Retrieval quality and request-time access

### 3.1 Embedders: the biggest single quality lever

| Model | Params | Dims (MRL) | Ctx | Signal | ONNX | License |
|---|---|---|---|---|---|---|
| all-MiniLM-L6-v2 *(current)* | 22M | 384 | 256 | ~56 MTEB-en (v1) | ✓ | Apache-2.0 |
| **EmbeddingGemma-300M** (Sep 2025) | 308M | 768 → 512/256/128 | 2048 | MMTEB-en 69.7, code 68.8 (768d); 128d still 65.1/64.3 | official (fp32/q8/q4, **no fp16**) | Gemma terms |
| Qwen3-Embedding-0.6B (Jun 2025) | 600M | up to 1024, MRL | 32k | MMTEB ~64 (vendor) | community | Apache-2.0 |
| granite-embedding-english-r2 / small (Aug 2025) | 149M / 47M | 768 / 384 | 8192 | BEIR 53.1 / 50.9 | ✓ | Apache-2.0 |
| nomic-embed-text v1.5 / v2-moe | 137M / 475M | 768 → 64/256 | 8k / 512 | BEIR 52.9 (v2) | ✓ | Apache-2.0 |
| **potion-retrieval-32M** (model2vec, static) | 32M | 512 | ∞ | ~92% of MiniLM on MTEB avg (vendor) | not needed (lookup + mean pool) | MIT |
| CodeRankEmbed | 137M | 768 | 8k | strong on code search | ✓ | Apache-2.0 |
| **granite-embedding-97m-multilingual-r2** (Apr–May 2026) | 97M | 384 (no MRL) | 32k | Eng retrieval 50.1, code 60.4, multilingual 60.3 | **shipped** (q-ONNX ≈98 MB) | Apache-2.0 |
| snowflake-arctic-embed-m-v2.0 (Dec 2024) | 305M | 768 → 256 | 8k | BEIR 55.4 (54.4 @256) | official | Apache-2.0 |
| Harrier-OSS-v1-270m (Microsoft, Mar 2026) | 270M | 640 | 32k | MMTEB v2 66.5 | none official | MIT |
| jina-embeddings-v5-text-nano (Feb 2026) | 239M | 768, MRL | 8k | Eng v2 71.0 | ✓ | **CC-BY-NC** |

Three caveats:

- jina v3/v4/v5, jina-code, and jina-reranker are **CC-BY-NC**, so they are out
  for distribution.
- **hugot's pure-Go backend is about 10× slower than ONNX Runtime.** The
  query-time latency of a 300M model under GoMLX is **unmeasured**. A static
  model (potion) can be implemented in roughly 150 lines of Go with no ONNX at
  all, and makes a good instant tier or fallback.
- **Op coverage is the real constraint, not quality.** onnx-gomlx says "not all
  ops are converted yet" and confirms only MiniLM. Nothing confirms that the
  ModernBERT (Granite R2, Ettin), Gemma3 (EmbeddingGemma, Harrier), or Qwen3
  graphs run. So the **first step of the bake-off is a load-and-embed smoke test
  per candidate**, and candidates that fail it are dropped.
- **Code-specific embedders are low priority.** Small general models from
  2025–26 already score 60–71 on the MTEB code tasks.

### 3.2 Reranking: the second lever

[Anthropic's Contextual Retrieval study](https://www.anthropic.com/news/contextual-retrieval)
(Sep 2024) measured top-20 retrieval failure:

| Configuration | Failure reduction |
|---|---|
| Contextual embeddings | −35% |
| + contextual BM25 | −49% |
| + reranker | **−67%** |

Later studies of hybrid RRF found reranking adds +11–24% relative nDCG@10
([2604.01733](https://arxiv.org/abs/2604.01733)). Reranking mostly *reorders*
candidates and rarely surfaces new ones
([2608.00452](https://arxiv.org/abs/2608.00452)), so the recall of the fused
candidate set is still what limits the result.

Local reranker options, from the [HF Ettin benchmark](https://huggingface.co/blog/ettin-reranker)
(May 2026). Scores are MTEB-R nDCG@10 over top-100; throughput is PyTorch on a
desktop CPU:

| Reranker | Params | nDCG@10 | CPU pairs/s | Notes |
|---|---|---|---|---|
| `ms-marco-MiniLM-L-6-v2` | 22M | 0.508 | 144 | Same architecture as the embedder, so it is **known to load under GoMLX**. Try it first |
| **Ettin-17M** | 18M | 0.558 | **267** | Apache-2.0, 8K context, ModernBERT-style. Its op coverage is unverified |
| **Ettin-32M** | 33M | 0.578 | 93 | Apache-2.0 |
| granite-reranker-english-r2 | 149M | 0.566 | 15 | Apache-2.0 |
| `mxbai-rerank-base-v2` | 0.5B | 0.592 | 3.5 | Apache-2.0; too slow for pure Go |
| `Qwen3-Reranker-0.6B` | 0.6B | n/a (scored on its own setup) | slow | Apache-2.0; LLM-style scoring |

Late interaction (ColBERT, or MUVERA [2405.19504](https://arxiv.org/abs/2405.19504))
is not worth its storage and code cost at this scale.

### 3.3 Chunking

- **Consensus.** Split structure-aware and recursively (headers → paragraphs →
  sentences) into chunks of about 200–400 tokens with modest overlap. Prepend
  title and section context to the text that gets embedded *and* indexed, and
  return the parent section at read time ("small-to-big").
- **Semantic chunking:** gains are inconsistent and not worth the cost
  ([2410.13070](https://arxiv.org/abs/2410.13070)).
- **Late chunking** ([2409.04701](https://arxiv.org/abs/2409.04701)): gives
  chunks document context without an LLM, but needs token-level outputs. Whether
  hugot supports that is unverified.
- **Contextual retrieval without a server LLM:** prepend `title > section path`
  (plus entity labels once they exist), and accept an optional client-written
  `context` string per document.
- **Propositions** (Dense X, [2312.06648](https://arxiv.org/abs/2312.06648)) help
  fact lookup. They map naturally onto client-supplied facts (§1, D-C). The gain
  is +10 Recall@20 for unsupervised retrievers but only +2 for supervised ones,
  and it fades as context budgets grow.
- **RAPTOR-style trees** lost to vanilla RAG by 2–8 points at budgets above
  5K tokens with GPT-4o ([2506.03989](https://arxiv.org/abs/2506.03989),
  EMNLP 2025).
- **Small corpora do not need RAG at all.** Anthropic advises skipping retrieval
  under about 200K tokens. A namespace that small can be served whole, as
  `granularity=document` or as an export resource, and the token budget decides.

### 3.4 Query time: the agent is the router

- HyDE, multi-query, Adaptive-RAG
  ([2403.14403](https://arxiv.org/abs/2403.14403)), Self-RAG, and CRAG all need
  an LLM. In MCP the **client is that LLM**. The server's job is to:
  - accept `queries[]` and fuse them (RAG-Fusion style);
  - expose a few clearly distinct modes;
  - return **calibrated relevance** so the agent can tell when to retry.
- **Server-side query expansion is not worth adding.**
  - The stronger the retriever, the less LLM expansion helps. Weller et al.
    ([2309.08541](https://arxiv.org/abs/2309.08541)) found a strong negative
    correlation across 24 retrievers.
  - HyDE's gains may come partly from benchmark leakage
    ([2504.14175](https://arxiv.org/abs/2504.14175)).
  - `queries[]` stays, but only because the *agent* chooses to send several
    queries.
- **Agentic iteration over simple tools beats clever one-shot retrieval.**
  - **A-RAG** ([2602.03442](https://arxiv.org/abs/2602.03442), Feb 2026) gives
    the agent three tools: `keyword_search`, `semantic_search` over sentence
    snippets, and `chunk_read(ids)` with ±1 neighbours. It beat naive RAG by
    **+21–39 points** on multi-hop QA while reading *fewer* tokens. The authors
    recommend "agent-friendly interfaces rather than complex retrieval
    algorithms."
  - AgenticRAG ([2605.05538](https://arxiv.org/abs/2605.05538)) reports
    recall@1 rising from 8% to 43–50% at 2–3× the tokens.
  - This is the strongest support for the `search` → `read` design in §6.3.
- **Coding agents: lexical and semantic search are complementary.**
  - Claude Code chose agentic grep over vector RAG.
  - [Cursor (Nov 2025)](https://cursor.com/blog/semsearch) measured
    +12.5% answer accuracy from semantic search, with the best results coming
    from **semantic + grep together**.
  - Implication: keep exact and identifier matching first-class. Porter stemming
    mangles symbols, so add a non-stemmed or trigram FTS index.
- **Progressive disclosure:**
  - Anthropic's [context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
    and [Agent Skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)
    guidance, and llms.txt, all say the same thing: return lightweight
    identifiers and let the agent dereference them.
  - [Writing tools for agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
    recommends a `response_format: concise|detailed` enum (≈72 vs ≈206 tokens in
    their example).

### 3.5 Request-time configurability: how others expose it

| System | Pattern |
|---|---|
| **Vespa** | Named **rank profiles** with typed per-query inputs; multi-phase ranking |
| **Elasticsearch** | Composable `retriever` tree (`knn`, `rrf`, `linear`, `text_similarity_reranker`) |
| **Weaviate** | `alpha`, fusion type, `maxVectorDistance`, `autocut`, rerank module |
| **Zep** | `scope` (edges/nodes/episodes), reranker enum (rrf/mmr/node_distance/cross_encoder), `center_node_uuid` |
| **Mem0 v3** | `top_k`, `threshold`, boolean metadata filters, `rerank` (off by default, +200–400 ms); `keyword_search` removed in favour of one multi-signal score |
| **OpenSearch 3.x** | Per-request `search_pipeline`: RRF with weights, or min-max/l2/z-score linear fusion. On their BEIR test, RRF averaged **3.9% lower nDCG@10** than score fusion |
| **Vectara** | `lexical_interpolation`, `sentences_before/after` (granularity), reranker chain ending in a `knee()` cutoff and MMR |
| **OpenAI file_search** | `max_num_results`, `ranking_options.score_threshold`, attribute filters |
| **Context7** | Two-step `resolve-library-id` → `get-library-docs(topic, tokens)`, with a **token budget** |

Evidence on LLM tool use:

- Tool-selection accuracy collapses as the surface grows: 13.6% vs 43.1% with
  narrowing in RAG-MCP ([2505.03275](https://arxiv.org/abs/2505.03275));
  "Less is More" ([2411.15399](https://arxiv.org/abs/2411.15399)) shows the same
  effect.
- The design that converges across Vespa, Elastic, and Zep: **a small enum of
  named strategies plus a few orthogonal, typed, defaulted parameters**.
- Raw weights (`alpha`, `rrf_k`, half-life) belong in server-side profiles and
  the eval lab, **not in the LLM-facing schema**.
- **Absolute score thresholds are fragile.** Zep deprecated `min_score`, Mem0
  has changed its threshold defaults, and fused RRF scores top out around
  0.016. Gap-based cutoffs (Weaviate `autocut`, Vectara `knee()`) hold up better.
  Products are also moving from `k` toward **token or character budgets** (Zep
  `max_characters`, LightRAG token caps).
- **Description quality matters measurably.** In one study 97% of 856 MCP tools
  had description smells, and fixing them gave +5.9 pp task success
  ([2602.14878](https://arxiv.org/abs/2602.14878)). With complex parameters,
  examples in descriptions raised accuracy from 72% to 90% (Anthropic). MCP has
  no `input_examples` field, so put 1–2 examples in the description text.
- **MCP 2026-07-28 is stateless at its core**, so per-session "already read"
  tracking cannot live on the server. Instead the client passes `exclude_ids`.

**Decision drivers**

- **D-I (models).** Ship a `models` table.
  - **Priority:** MemDelta (§1.5) measured a 6.2 pp gain on LongMemEval-S from
    moving *off MiniLM* alone. That is more than separates Mem0 from plain RAG.
    This is the cheapest large win available, so the bake-off runs right after
    step A.
  - **Default:** a permissively licensed model (granite-r2 small or base, or
    Qwen3-Embedding-0.6B).
  - **Instant tier and fallback:** potion static.
  - **Continuity:** MiniLM.
  - **Opt-in:** EmbeddingGemma @ 256-d (q8), not the default because of its
    licence (§8, decision 4).

  Each model must win its place in `memo-mcp eval`, with latency measured under
  GoMLX.
- **D-J (reranking).** Add an optional cross-encoder rerank stage over the top
  20–40 fused candidates, promoted only if it improves nDCG@10.
  - Try ms-marco-MiniLM first, because it is known to load under GoMLX.
  - Then try Ettin-17M/32M if they pass the op-coverage smoke test.
  - Rerank is part of the `precise` profile and is never a raw LLM parameter.
- **D-J2 (fusion and cutoffs).** Keep weighted RRF as the default. Add a min-max
  linear-fusion profile and let the eval decide between them. Replace absolute
  thresholds with a **gap-based cutoff** inside the token budget.
- **D-K (access surface).** About seven defaulted parameters per tool, with named
  profiles for everything numeric (§6.3).

---

## 4. On-demand acquisition and MCP integration

### 4.1 How knowledge products for coding agents work

- **Context7**: version-pinned library snippets, resolve then fetch with a token
  budget.
- **DeepWiki**: generated wiki pages per repo, served through MCP as
  `read_wiki_structure`, `read_wiki_contents`, `ask_question`.
- **Ref.tools**: session-aware search that dedupes results already shown to the
  session.
- **GitMCP**: any repo on demand.
- **Aider repo-map**: a PageRank-ranked symbol map that fits a token budget.
- **basic-memory**: markdown plus a SQLite index.

They converge on three things:

1. **Resolve → fetch under a budget.**
2. **Version pinning.**
3. **Markdown as the storage and interchange format** (llms.txt, AGENTS.md,
   Skills, DeepWiki pages).

One more data point points the same way: [Vercel's eval](https://vercel.com/blog/agents-md-outperforms-skills-in-our-agent-evals)
(Jan 27 2026). An **8 KB docs index in AGENTS.md scored 100%**, while Skills
scored 53% by default and 79% with explicit instructions. The reason is that
agents didn't invoke the skill in 56% of cases. This argues for a passive,
file-shaped face alongside the tools.

Other 2026 market signals:

- **Docfork shut down** on 2026-06-14. It had BM25 + vectors with RRF,
  AST-aware chunks, and pinned versions.
- **Context7** renamed `get-library-docs` to `query-docs`, and added a
  CLI + Skills mode that needs no MCP.
- Mintlify's ChromaFs (Apr 2026) emulates `ls/cat/grep` over a vector DB.
  Filesystem-shaped access is converging from several directions.
- Counter-evidence: Arize found a SQL skill (99/100) beat a fake filesystem
  (93/100).
- The AWS study ([2602.23368](https://arxiv.org/abs/2602.23368)) found that
  keyword-only agents reach about 90% of RAG.

**Version keying is the product.** Context7 keys by git tag
(`/vercel/next.js/v15.1.8`), and Next.js 16.2 ships version-matched docs inside
`node_modules`. GitChameleon 2.0 ([2507.12367](https://arxiv.org/abs/2507.12367))
shows that even *with* retrieved docs, version-specific code generation tops out
at 58.5%. So `(namespace, library, version)` must be a first-class filter that
applies **before** top-k, and the agent's manifest (`go.mod`, `package.json`)
picks the version.

### 4.2 The ingestion pipeline

- **Deep-research patterns.** The closest analogue to "on-demand research that
  produces structured knowledge" is **STORM / Co-STORM** (Stanford, 2024).
  STORM generates questions from multiple perspectives, builds an outline, and
  writes an article with citations. Co-STORM maintains a dynamic mind map.
  Anthropic's multi-agent Research system uses an orchestrator with workers
  ([Jun 2025](https://www.anthropic.com/engineering/multi-agent-research-system)).
- **Acquisition is done client-side.** The agent already has web fetch/read
  tools and passes markdown plus a source URI. The server does not fetch (§8,
  decision 2). The ingestion research suggested a server-side `learn(url)` with
  ETag checks; that conflicts with decision 2 and is not adopted. The client
  sends `source.version` and `content_hash` instead. For reference only: a
  pure-Go fetch path would have been `codeberg.org/readeck/go-readability/v2` +
  `JohannesKaufmann/html-to-markdown` v2. Docling (best structure; 77 vs 57 on a
  Jul 2026 PDF benchmark), MarkItDown, and Crawl4AI are Python and would stay
  out of the binary either way. Crawl4AI also had several Docker RCE/SSRF CVEs
  in 2026. The recommended **client-side source order** is llms.txt /
  llms-full.txt, then the repo docs folder at the matching git tag, then a
  crawl. llms.txt is a useful source when present but is not a discovery
  mechanism: Ahrefs found 97% of such files got zero requests.
- **Code-aware chunking.** AST chunking (cAST,
  [2506.15655](https://arxiv.org/abs/2506.15655)) beats line chunking: +4.3
  Recall@5 on RepoEval. **Pure-Go tree-sitter now exists:**
  - `odvcencio/gotreesitter`: no CGo, 206 grammars.
  - `malivvan/tree-sitter`: the C runtime compiled to Wasm and run on wazero.

  Evaluate one of them for code chunking. Until then, use fenced-block- and
  heading-aware splitting, with `go/parser` for Go code. Maturity of both ports
  is unverified.
- **Provenance and freshness.** Store `source_uri`, `fetched_at`,
  `content_hash`, `etag`, and `version` (semver or git SHA). Re-ingest on TTL
  expiry or hash mismatch, and **mark derived pages stale** when a source they
  were built from changes.

### 4.3 Security: memory poisoning is real and well documented

| Attack | Result |
|---|---|
| **MINJA** ([2503.03704](https://arxiv.org/abs/2503.03704)) | >95% injection success using queries alone |
| **AgentPoison** ([2407.12784](https://arxiv.org/abs/2407.12784)) | ~80% attack success at <0.1% poisoning |
| **PoisonedRAG** (USENIX Sec 2025) | 5 texts → ~90% success |

Related work: MCP tool-poisoning and "rug pulls" (Invariant Labs, Apr 2025), and
Simon Willison's **lethal trifecta** of private data + untrusted content +
exfiltration channel.

2026 additions:

- **ContextCrush** (CVE-2026-75130, disclosed Mar 2026). Context7 served
  library-owner "Custom Rules" unfiltered next to docs, and a demo exfiltrated
  `.env` files.
- **"From Untrusted Input to Trusted Memory"**
  ([2606.04329](https://arxiv.org/abs/2606.04329)) found that prompt-injection
  defences do not cover memory poisoning, and that aggressive memory writers
  are easier to exploit.
- **"Revoked but Still Authoritative"**
  ([2609.08258](https://arxiv.org/abs/2609.08258), Sep 2026) found that none of
  five memory systems enforces revocation by default. Revoked facts often
  **outrank** their replacements.
- Graphiti's MCP server fixed a Cypher injection in v1.0.2 (Mar 2026).

For a store that ingests web content and serves it to agents with tools, this is
the main risk. Mitigations:

- **Trust tiers** recorded on every record.
- Retrieved text is wrapped and labelled as *data*.
- Web-ingested content never lands in a high-trust tier automatically.
- **Never serve instruction-shaped fields** ("rules", "system notes") from
  ingested sources as anything other than quoted content.
- **Revocation is a hard filter.** Tombstoned, superseded, and invalidated
  records are excluded *before* ranking by default. They are reachable only
  through an explicit `as_of` query, never by a soft score penalty.
- Namespace isolation.
- An audit log.
- **An eval slice for poisoning and revocation**, so regressions are measured.

### 4.4 MCP features relevant to this project

**The 2026-07-28 spec is final.** Sources: the
[changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)
and the [launch post](https://blog.modelcontextprotocol.io/posts/2026-07-28/).
- **Stateless core:** there is no `initialize` handshake and there are no
  sessions. Servers implement `server/discover`.
- **Multi Round-Trip Requests (MRTR)** replace server→client requests:
  `input_required`, then the client retries.
- List and read results carry `ttlMs` and `cacheScope`.
- Schemas can use full JSON Schema 2020-12.
- Tasks moved to an extension.
- **Sampling, Roots, and Logging are deprecated** (SEP-2577). Removal comes no
  sooner than 12 months later.

**Go SDK:**
- v1.7.0 (2026-07-28) supports the new spec; over HTTP only with
  `Stateless=true`.
- v1.8.0 (2026-09-14) adds `SetCacheable`, `SupportedProtocolVersions`, and
  `NotifyElicitationComplete`.
- **memo-mcp pins v1.6.0.**

| Feature | Status | Use here |
|---|---|---|
| `outputSchema` / `structuredContent` | 2025-06-18; JSON Schema 2020-12 in 2026-07-28 | Citation-ready, typed search results. Always add a text mirror in `content`: OpenAI asks for it, and ChatGPT only cites hits that have a non-empty `url` |
| Resources + templates | stable; `subscriptions/listen` replaces `resources/subscribe` | `memo://…` URIs for documents, chunks, and pages. Claude Code supports `@server:uri`, but its **template support is unclear**. Put core capability in tools and mirror it as resources |
| `ttlMs` / `cacheScope` | 2026-07-28 | Freshness hints on list and read results (via `SetCacheable` in v1.8.0) |
| Tool annotations | 2025-03-26 | `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint: false` on every tool |
| **Tasks** | extension (`io.modelcontextprotocol/tasks`) | Not in Go SDK v1.8.0 and not in the client matrix yet. Use a **job handle + `status` tool**, which the spec endorses as server-minted handles passed back as arguments |
| **Sampling** | **Deprecated** (SEP-2577); Claude Code never shipped it | **Do not depend on it.** The SEP itself says servers that need an LLM should call a provider API directly, which matches D-L |
| Elicitation | 2025-06-18; carried over MRTR in 2026-07-28 | Trust promotion (§8.1) and destructive confirmations |
| **Skills extension** (`io.modelcontextprotocol/skills`, SEP-2640 Final) | Go SDK PR open (go-sdk#1238); partial client support | Ship a "how to use this KB" skill. Also ship a plain `SKILL.md` for other clients |
| MCP Apps (`io.modelcontextprotocol/ui`) | Claude web/Desktop, Cursor, VS Code, ChatGPT | Optional curation/graph UI for chatbots later; irrelevant to coding agents |
| Anthropic `search_result` blocks | GA | MCP has **no `search_result` content type**, and clients don't map tool results onto it. Shape `structuredContent` the same way (`source`, `title`, `content[]`). SEP-3094 "Granular Citations" is an open PR to watch |
| Registry | preview, API frozen at v0.1 | Publish in step H, expecting changes |

**Decision drivers**

- **D-L (LLM placement).** LLM work happens **in the client, through tool
  loops**. Local Ollama is opt-in. Cloud is opt-in and breaks the no-network
  promise. Sampling is not used.
- **D-M (two faces).** One store, served two ways: **(a)** search and read tools
  that return citation-ready structured results, and **(b)** a file-shaped face
  (resources plus `memo-mcp export --md`) that agents can grep and that AGENTS.md
  or Skills can link to.
- **D-N (provenance and trust).** Provenance and trust go in the schema from the
  first migration that touches documents. Revocation is a hard pre-ranking
  filter.
- **D-O (SDK and spec).** Upgrade go-sdk from v1.6.0 to **v1.8.0** and serve
  2026-07-28 on stdio. Use `Stateless=true` if HTTP is ever added. Rely on no
  server-side session state; `exclude_ids` and job handles travel as arguments.
  Do not use sampling, roots, or logging.

---

## 5. Consensus vs. contested: summary

| Settled enough to build on | Still contested; measure before committing |
|---|---|
| Keep raw sources; every derived record cites them | Whether write-time LLM extraction beats raw + agentic search on accuracy |
| Hybrid lexical + dense retrieval, plus reranking | Whether graph structure improves accuracy, or only enables relational and temporal queries |
| Structure-aware ~200–400-token chunks with context headers | Late chunking vs. context headers |
| Bi-temporal validity; invalidate rather than delete | Optimal compaction granularity (entity pages vs. communities vs. trees) |
| Calibrated relevance and **abstention** | Benchmark validity: LoCoMo is saturated and vendor numbers don't reproduce |
| Progressive disclosure: identifiers first, content on demand | Static-embedding quality on technical and code text |
| Graph helps multi-hop/global questions and hurts simple lookup, so **route** | — |
| Memory poisoning is a real attack surface | — |

---

## 6. Target architecture

### 6.1 Knowledge layers

Each layer derives from the ones below it and keeps provenance links down to
them.

```
L5  pages       derived markdown: entity pages, topic/community pages     (compaction, client-LLM written)
L4  graph       entities, aliases, mentions, bi-temporal edges             (heuristic + client-supplied)
L3  facts       atomic claims, bi-temporal, supersession chains           (client-supplied; extra retrieval keys)
L2  chunks      ~200–400 tok, context header, FTS (stemmed + exact), vec  (deterministic)
L1  documents   normalised markdown, revisioned                           (ingest / remember)
L0  sources     uri, version, content_hash, fetched_at, trust, namespace  (provenance root)
```

- **L0–L2 are deterministic and LLM-free.** They are always correct and always
  rebuildable from scratch.
- **L3–L5 are optional and additive.** Every row carries `source_chunk_ids` and
  the revision it was built from, so it can be invalidated when the source
  changes.
- **Facts work as keys, not replacements** (LongMemEval). A matched fact is
  returned *with* its evidence chunk.

### 6.2 Schema sketch (additive migrations on the existing scaffold)

```sql
sources   (id, namespace, uri, title, kind /*doc|note|code|conversation*/,
           library /*e.g. "vercel/next.js"; nullable*/, version /*semver|git tag|SHA*/,
           content_hash, etag, fetched_at, ttl_s,
           trust  /*curated|user|agent — assigned by channel, §8.1*/,
           origin /*web|user-said|agent-derived — client-declared hint*/)
documents (id, source_id, revision, content, created_at, updated_at,
           deleted_at, superseded_by)            -- generalises today's `entries`
chunks    (id INTEGER PK, document_id, ord, section_path, text, context_header, est_tokens)
chunks_fts        -- external-content, porter
chunks_fts_exact  -- unicode61 or trigram, for identifiers
chunk_vecs (chunk_id, model_id, embedding)  ; models (id, name, dim, prompt_prefixes)

facts     (id, namespace, statement, subject_entity_id, valid_from, valid_to,
           recorded_at, invalidated_at, superseded_by, evidence_chunk_id, trust)
entities  (id, namespace, canonical, type, summary_page_id)
entity_aliases (alias, entity_id) ; mentions (entity_id, chunk_id)
edges     (src, dst, rel, weight, valid_from, valid_to, recorded_at,
           invalidated_at, evidence_chunk_id)
pages     (id, kind /*entity|topic|overview*/, subject_id, content, built_at,
           built_from_rev, stale)   ; page_sources (page_id, chunk_id)
jobs      (id, kind /*ingest|reindex|compact*/, scope, state, created_at, items_json)
audit     (ts, actor, op, target_id, detail)
```

`MEMO_KB` (formerly `JOURNAL_TOKEN`) selects the database file. `namespace`
becomes a column inside it. By default `search` covers **all** namespaces in the
file (§8, decision 5): for example a library, a project, and personal notes
together. Writes always target exactly one namespace.

Trust and origin are separate columns. `trust` is assigned by channel and cannot
be forged through tool calls. `origin` is what the client declared. See §8.1.

### 6.3 Tool surface: 10 tools (`explore` arrives in step F), about 7 parameters each, all defaulted

**Write**

- `ingest(content, source{uri,title,version,kind,origin}, namespace?, context?)`
  stores the document, chunks it, and indexes it. The client fetches; the server
  never does (§8, decision 2). Re-ingesting an identical `content_hash` is a
  NOOP; a changed hash creates a new revision and marks dependent pages stale.
- `remember(statement, about?[], valid_from?, supersedes?, evidence_uri?, namespace)`
  records an atomic fact. Facts are only ever added (as in Mem0 v3), and an
  explicit `supersedes` invalidates the old fact rather than deleting it (as in
  Graphiti).
- `forget(id, reason, redact?)` creates a tombstone.
- `promote(id, to)` raises trust, but only after the human accepts an
  elicitation; otherwise it points to the CLI (§8, decision 6).

**Read**

- `search(...)`, shown below.
- `read(uri, max_tokens?, granularity?)` dereferences any `memo://` URI
  (document, chunk, fact, entity, page). The same objects are exposed as MCP
  resources.
- `explore(entity, hops=1, as_of?)` returns an entity's neighbourhood with
  facts and evidence. It is added in the graph phase.

**Maintain**

- `compact(scope, kind)` returns work items such as "here are 14 chunks about X;
  write or update its page" or "these 3 facts conflict; resolve them".
- `submit(item_id, result)` stores the result as an L5 page or an L3 resolution,
  with provenance.
- `status()` covers stats, pending embeddings, stale pages, and jobs, and
  replaces `journal_stats`.

The legacy journal tools (`process_thoughts` → `ingest` with `kind=note`, and
so on) are registered only when `MEMO_LEGACY_TOOLS=1` is set, for one release
(§8, decision 1).

```text
search(
  query | queries[]                                   # multi-query, fused with RRF
  mode:        auto|hybrid|keyword|exact|semantic|graph = auto
  scope:       {namespaces[], kinds[], sources[], library, version, tags[], date_from,
                date_to, min_trust}                          # applied before top-k
  granularity: chunk|document|fact|page             = chunk   # small-to-big / compaction level
  as_of:       timestamp?                                     # bi-temporal "what was true then"
  response_format: concise|detailed                 = concise # IDs + titles + 1-liners first
  max_tokens:  int                                  = 2000    # server packs results to budget
  exclude_ids: [uri]?                                         # already-read results; MCP is stateless
)
```

How `search` behaves:

- **`mode=auto`:**
  - Identifier-looking queries (`::`, `.`, `_`, camelCase, error codes) route to
    `exact` + hybrid.
  - Queries naming two or more known entities, or using relational phrasing,
    add the `graph` arm.
  - Everything else runs hybrid.
- **Numeric knobs** (RRF k, arm weights, half-life, rerank on/off, fetch
  depth) live in **named server-side profiles** such as `default`, `recency`,
  `precise`, and `code`, Vespa-style. They are tuned in the eval lab. A
  `profile` enum is exposed only if the eval shows profiles matter per request.
- **Results** are `search_result`-shaped structured content:
  - `uri`, `title`, `section_path`, `content`;
  - `relevance` (raw cosine) plus a band (`strong`/`moderate`/`weak`). The
    list is cut at the first large score gap (autocut-style), never at a fixed
    threshold;
  - `trust`, `version`, `stale`;
  - a `degraded` flag, and a next-step hint when matches are weak, which
    supports abstention.
- **Truncation is explained.** When the budget cuts results off, a footer says
  how many remain and which `scope` field would narrow the search.
- **Retrieved text is wrapped as data**, never as instructions.

### 6.4 Where computation happens

| When | What | LLM? |
|---|---|---|
| **Write (sync)** | normalise → chunk → context header → FTS → embed (or queue) → hash/dedup → heuristic entity mentions → mark dependent pages stale | No |
| **Write (client-supplied)** | `context`, `facts[]`, `entities[]`, `relations[]`, `supersedes` | Client's |
| **Query** | hybrid arms → optional graph PPR arm (gonum, cached adjacency) → RRF → optional rerank → small-to-big → budget packing | No |
| **Compaction (explicit job)** | near-duplicate and conflict detection; Louvain topics; entity and topic page *work items*; staleness sweep. Triggered **by recurrence** (RecMem: only clusters that keep growing), so cost stays bounded. Results are add-only, `is_inference`-flagged, and shown as a dry-run diff before they land. Raw rows are never touched | Client's (via `compact`/`submit`), or opt-in Ollama |
| **Background** | embedding backfill, TTL-based re-ingest *proposals* (never automatic fetches) | No |

### 6.5 Two faces of one store

- **Chatbots** use `search` with `granularity=page|fact`, `detailed` output,
  and citations.
- **Coding agents** use:
  - `search` with `mode=exact|auto`, a `version` scope, and `concise` output,
    then `read(uri, max_tokens)`;
  - **or** `memo-mcp export --md <dir>`, which writes one file per page and
    document with front-matter provenance. AGENTS.md or Skills can link it, and
    plain grep works on it. This also covers "passive context beats tools the
    agent forgets to call."
  - **`memo-mcp export --index [--ns … --library …@…]`** writes an index of at
    most 8 KB, sized to be pasted into or linked from AGENTS.md or CLAUDE.md.
    This is the shape that scored 100% in Vercel's eval (§4.1).
- **Both** can load a compact per-namespace index resource
  (`memo://ns/{namespace}/index`) at session start. It has `MEMORY.md`-style
  one-liners per page or topic and mirrors Claude Code Auto Memory and Letta
  Context Repositories.

---

## 7. Mapping onto the existing roadmap

Phases 0–1 of the 2026-09-04 review are done in substance (fixes, fake
embedder, golden-set eval, MCP golden tests; see [`roadmap.md`](roadmap.md) §4
for what shipped and what is carried over). Recommended order from here, with
each step gated on `make eval` showing no regression. **The live, phased plan
is [`roadmap.md`](roadmap.md)**; the mapping from these steps to its phases is
A → P1/P2, B → P1 (schema) and P4 (semantics), C → P3, D → P3 onward,
E → P0 (SDK) and P2/P5 (surface), F → P4 (fact arm) and P7, G → P8, H → P6.

| Step | Contents | Relation to roadmap |
|---|---|---|
| **A. Chunks** | `internal/chunk`, `chunks`, external-content FTS (stemmed + exact), chunk vectors, `models` table with dim as a parameter, backfill worker | Roadmap Phase 2 unchanged, plus the exact-match FTS and the `models` table pulled forward |
| **B. Sources & time** | `sources` (provenance, trust, version, hash), `documents` generalising `entries`, `namespace` column, bi-temporal fields, `ingest` / `remember` / `forget` | Merges roadmap Phase 4 with D-B and D-N |
| **C. Lab** | **First, a GoMLX op-coverage smoke test per model.** Then the embedder bake-off: granite-r2 small/base, granite-97m-r2, arctic-m-v2, Qwen3-0.6B, potion, MiniLM, and opt-in Gemma-256d, with **GoMLX latency measured**; the default must be Apache/MIT. Optional reranker (ms-marco-MiniLM, then Ettin-17M/32M). Named profiles, including RRF and linear fusion. `memo-mcp eval --strategy … --profile …` | Roadmap Phase 3 |
| **D. Eval expansion** | Add LongMemEval-style categories: **knowledge update, temporal/as-of, abstention, multi-hop, version-pinned code lookup, conflict, cross-namespace**. Add a cost column (ms, tokens returned, write-path cost). Add a **"raw chunks + agent iterating" baseline** and a plain BM25 baseline that every L3–L5 feature must beat. Hold the embedder fixed when comparing architectures (MemDelta). Measure **write loss** separately from **retrieval loss** (WhenLoss), and check derived records for omission and corruption (TRUSTMEM). Plant validity intervals first, Veracium-style | New; **gates E–G** |
| **E. Access surface** | go-sdk v1.6.0 → v1.8.0 with the 2026-07-28 spec (D-O). `search` v2 (§6.3), `read`, resources with `ttlMs`, structured `search_result`-shaped output plus a text mirror, annotations, budget packing, `export --md` / `--index`. A "using memo" `SKILL.md` | Roadmap Phase 5's annotations/outputSchema/resources, retargeted |
| **F. Facts & graph** | Facts as extra keys. **Then an entity-match arm in RRF** (Mem0 v3 / Graphiti / Supermemory all have one; it is cheap and needs no edges). Then heuristic + client-supplied entities with deterministic resolution (D-H2), aliases, **entity↔chunk mention edges** (rung 1), a PPR arm over an in-memory CSR with hub penalisation, `explore`, and routing in `auto`. Typed entity↔entity edges are optional rung 2. Eval includes an **update-stream test**: index half the corpus, add the rest in batches, and check that old queries don't regress. Graph edges ship only if they beat the entity arm on the multi-hop eval: Mem0g gained only ~1.5 pp before Mem0 removed it from OSS | Replaces `history/graphrag-evolution-plan.md` Phases 1–3 |
| **G. Compaction** | `compact` / `submit` jobs: dedup/conflict items, `merge_candidates`, Louvain topics (only if the eval contains global questions), entity/topic pages, a **lint** pass (contradictions, orphans, stale claims), staleness sweep; optional Ollama executor (non-thinking model, constrained JSON) | Replaces old plan's Phase 5; new |
| **H. Ship** | CI, GoReleaser, registry | Roadmap Phase 6 |

**Ordering note (from MemDelta, §1.5).** The embedder bake-off in step C
needs only step A's `models` table. Run it **immediately after A, before B**,
because it is probably the largest single accuracy lever available and it
changes the baseline every later step is measured against.

Two pieces of the old GraphRAG plan stay **deferred or dropped**:

- The cross-namespace "global graph". In-database namespaces plus per-request
  `scope.namespaces` cover the need without a second database.
- Keyword/TF-IDF as the *only* extractor.

### What not to build

These are explicitly out of scope, with the reason for each:

- **Server-side LLM pipelines as a requirement.** They break the no-network
  promise, and the client already is an LLM.
- **GraphRAG-style global community summarisation at index time.** It updates
  poorly and costs 10–1000× more.
- **Semantic chunking, ColBERT/MUVERA, server-side HyDE or Self-RAG loops.** The
  evidence doesn't justify them at this scale.
- **Embedded graph databases** (Kuzu was archived; the others need CGo).
- **Depending on MCP sampling.**
- **LLM-judge win rates as the primary metric.** They are biased; use labelled
  recall/nDCG plus cost.

---

## 8. Decisions

### Taken (2026-10-01)

1. **Rename journal → knowledge. Yes.**
   - The tool surface is the one in §6.3 (`ingest`, `remember`, `forget`,
     `promote`, `search`, `read`, `explore`, `compact`, `submit`, `status`).
   - The README pitch becomes "measurable local knowledge base for agents".
   - *Consequence:* the six legacy journal tools are **not** registered by
     default. Registering them alongside the new tools would roughly double the
     tool surface, and §3.5 shows that measurably hurts tool selection. They are
     available behind `MEMO_LEGACY_TOOLS=1` for one release, then removed.
   - The env var follows the rename: `MEMO_KB` selects the database file, and
     `JOURNAL_TOKEN` stays accepted as a fallback with a deprecation warning.
     The default path moves only if the old one doesn't exist.
2. **The server does not fetch URLs.**
   - `ingest` takes `content` only. The client fetches and normalises; the
     server records the `source.uri` the client reports.
   - The "only network call is the model download" promise stands.
   - `go-readability` / `html-to-markdown` (§4.2) are out of scope.
3. **Local LLM executor: optional.**
   - Compaction works fully through the client (`compact` → `submit`).
   - An Ollama executor that drains work items unattended is an opt-in extra
     (`MEMO_OLLAMA_URL`, loopback only by default). It is never required, and no
     code path depends on it.
4. **EmbeddingGemma: optional, not default.**
   - The default embedder must be permissively licensed (Apache-2.0/MIT).
   - EmbeddingGemma is offered as an opt-in model whose Gemma-terms licence is
     shown at download time.
   - Default candidates for the step-C bake-off:
     - granite-embedding-small-english-r2 (47M, 384-d, Apache-2.0; same
       dimension as today, so no vector migration is needed to try it);
     - granite-embedding-english-r2 (149M, 768-d);
     - Qwen3-Embedding-0.6B (Apache-2.0; heavier on CPU);
     - potion-retrieval-32M (MIT) as the instant tier;
     - MiniLM as the incumbent.
5. **`scope.namespaces` defaults to all namespaces in the file.**
   Consequences, all required:
   - **(a)** Every result carries its `namespace`.
   - **(b)** Write tools still need an explicit target namespace, or fall back
     to a configured default (`MEMO_DEFAULT_NAMESPACE`). Writes never fan out.
   - **(c)** Fusion stays rank-based (RRF), so namespaces of very different
     sizes don't need score normalisation. The eval gains a cross-namespace
     query set to check that a large namespace doesn't drown out a small one.
   - **(d)** Trust labels matter more, since a poisoned namespace now reaches
     every query by default. See decision 6.

6. **Trust model: option B (elicitation where available, CLI always).**
   Reasoning is in §8.1.
   - **Writes:** tool writes are capped at `trust=agent`, with the
     client-declared `origin` stored separately.
   - **`promote(id, to)` tool:** applies only after the human accepts an MCP
     elicitation showing the excerpt, source URI, declared origin, and target
     level. On clients without elicitation support (Claude Desktop, claude.ai as
     of this writing), it returns the equivalent `memo-mcp trust promote <id>`
     command instead of acting.
   - **CLI:** `memo-mcp trust list|promote|demote` and
     `memo-mcp import <dir> --trust curated` are always available.
   - **Audit:** every promotion goes to the `audit` table, recording the channel
     (`elicitation` or `cli`).
   - **Caveat (document in the README):** a user-configured Claude Code
     `Elicitation` hook can auto-accept these dialogs. That is the user's choice
     and it disables the protection.

### 8.1 Decision 6: what "trust" is, and why promotion matters

**What a trust level does.** Every record (source, document, fact, page)
carries a trust level, and it affects four things:

| Effect | Example |
|---|---|
| **Labelling** | Results show `trust: web`, so the agent (and the user reading its answer) can weigh them |
| **Filtering** | `search(scope.min_trust=user)` excludes everything below that level |
| **Conflict resolution** | When `compact` finds two contradicting facts, the higher-trust one wins by default, and the lower one is invalidated, not deleted |
| **Compaction input** | A `curated` entity page can only be rewritten from inputs at or above a chosen level. Web content can propose a change, but can't silently overwrite curated knowledge |

**The key constraint, which follows from decision 2.** The server never fetches
anything, so *every* write arrives through a tool call made by the agent. The
server cannot tell whether content came from the user, from a web page the
agent read, or from text the agent made up. **Any trust level declared in a
tool call is self-reported.**

That matters because of how MINJA-style poisoning works (§4.3). Injected text
gets the agent to call `remember` or `ingest` itself. If the agent can label its
own write as `curated`, or promote it, the trust system protects nothing.

**So the question isn't really "who promotes".** It is: **which channel can
create or raise trust that the agent cannot forge?** There are three candidate
channels:

| Channel | Forgeable by an injected agent? | Notes |
|---|---|---|
| Tool call, self-declared | **Yes** | Fine as a *hint* (`origin: user\|web\|agent` stored as declared), useless as protection |
| **MCP elicitation** (the server asks the human directly through the client) | No: the human clicks Accept in the client UI, and the model can't answer it | Claude Code supports it since v2.1.76 (Mar 2026). **Claude Desktop and claude.ai don't yet.** It can be auto-answered by a user-configured `Elicitation` hook, but that is the user's own choice |
| **CLI / file import** (`memo-mcp trust …`, `memo-mcp import <dir> --trust curated`) | No: it runs outside the model loop | Always available; more friction |

**Options**

- **A. CLI only.** Tool writes are capped at `agent` (with the declared origin
  stored as a hint). Raising trust happens only through `memo-mcp trust promote
  <id>` or `memo-mcp import --trust curated`. This is maximum safety and works in
  every client, but the user has to leave the chat to curate.
- **B. Elicitation where available, CLI always** *(recommended)*. Same cap as A.
  A tool `promote(id, to)` additionally exists, and the server only applies it
  after an elicitation the human accepts. It shows the content excerpt, source
  URI, and target level. On clients without elicitation the tool returns
  "confirm with `memo-mcp trust promote <id>`". This keeps the safety of A with
  in-chat convenience in Claude Code.
- **C. Agent may promote freely.** The simplest option, but trust becomes purely
  cosmetic, and with decision 5 a single poisoned write can be ranked as
  authoritative across every namespace.
- **D. No promotion; trust is fixed by channel.** Tool writes are `agent`; CLI
  imports are `curated`; nothing changes afterwards. The simplest safe model.
  Correcting a level means re-importing.

**Default trust levels** (suggested for any of A, B, D):

- `curated`: CLI import or confirmed promotion only.
- `user`: elicitation-confirmed writes.
- `agent`: every tool write.
- The declared origin (`web` / `user-said` / `agent-derived`) is kept as a
  separate *origin* field, so labelling still works even though it doesn't
  confer trust.

`search` does **not** filter by trust by default (recall first). It does label
every result, and conflict resolution and compaction respect the levels.

---

## 9. Unverified or flagged claims

Re-verify each of these before building on it or citing it:

- **MCP 2026-07-28:** now **confirmed** by the official changelog and launch
  post: stateless core, `server/discover`, MRTR, `ttlMs`, Tasks moved to an
  extension, and the sampling/roots/logging deprecation in SEP-2577. Go SDK
  v1.7.0 and v1.8.0 release notes were read as well.
- Still open:
  - Go SDK support for the Tasks extension. It appears to be absent, but that
    rests on the release notes plus a third-party issue.
  - Claude Code support for resource templates.
  - SEP-3094 contents (only its title has been seen).
- Maturity of `gotreesitter` / `malivvan/tree-sitter`; the Context7 CVE fix
  status; Mem0 OpenMemory sunset (third-party report).
- EmbeddingGemma and Qwen3 CPU latency under hugot's GoMLX backend (no published
  numbers); whether hugot exposes token-level outputs (needed for late
  chunking).
- potion-retrieval-32M and Qwen3-Embedding MTEB figures (vendor-reported).
- The Letta filesystem LoCoMo figure (~74%, from their blog); the Mem0 and Zep
  numbers on both sides of the dispute.
- KAG, Triplex, ArchRAG, and NodeRAG gains (self-reported).
- The "graph helps ~15% of queries" figure in the 2026-09-04 review (now in
  git history: `git show 375f24c:docs/review-roadmap.md`; no primary
  source found; it is directionally consistent with
  [2502.11371](https://arxiv.org/abs/2502.11371) and
  [2506.05690](https://arxiv.org/abs/2506.05690)).
- The Kuzu archival date (reported Oct 2025).
- Vercel's AGENTS.md-vs-Skills eval details; AGENTS.md stewardship under the
  Linux Foundation.
- Claude Code "Auto Dream" consolidation details.
- §3 follow-up items:
  - **Which ONNX graphs hugot/GoMLX can run beyond MiniLM** (ModernBERT,
    Gemma3, Qwen3). This is the largest single risk for D-I and D-J.
  - The granite-small-english-r2 dimension (384 assumed).
  - Ettin CPU throughput, measured in PyTorch, not Go.
  - ConTEB contextual vs late-chunking figures (secondary source).
- §2 follow-up items: the LightRAG HotpotQA token count, the maturity of a
  Go Leiden implementation, GoMLX support for GLiNER/DeBERTa, and RAGSearch's
  corpus setup (graphs built per question, which favours the graph). LiteRAG,
  LinearRAG, and HIT-Leiden are single-paper results.
- §1.5 spot-checks: Mem0 v3 ADD-only + OSS graph removal, and the MemDelta
  numbers, **were checked against primary sources**. The other 2026 papers
  (WhenLoss, TRUSTMEM, SSGM, RecMem, MemMachine, LongMemEval-V2, Veracium,
  Hindsight) were reported by the research pass and not individually
  re-checked.
- Claude Managed Agents "dreaming" and ChatGPT "Dreaming V3" details (partly
  press-sourced; recall figures disagree across sources); Mem0 v3's two
  differing score sets (README vs migration doc); the Maximem audit (a vendor).
- Pure-Go tree-sitter availability.
