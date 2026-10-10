# GraphRAG Evolution Plan: memo-mcp

> **Renamed.** This document predates the rename to memors (2026-10-10): `memo-mcp` is now `memors-mcp`, `memo-tray` is `memors-tray`, `MEMO_*` is `MEMORS_*` and `~/.memo-mcp` is `~/.memors-mcp`. `memo://` addresses are unchanged.

> **Historical document — superseded (2026-10-01), moved to `docs/history/` on
> 2026-10-02.** The research synthesis
> [`../knowledge-base-sota.md`](../knowledge-base-sota.md) replaces this
> plan's approach (entity co-occurrence and "precedes" edges, keyword-only
> extraction, a separate cross-project global graph DB, graph on every query)
> with graph-as-index retrieval (personalised PageRank as one routed search
> arm), bi-temporal facts, and client-LLM-driven compaction. The live plan for
> that work is [`../roadmap.md`](../roadmap.md), phases P7 (entities and graph)
> and P8 (compaction and pages). Nothing below is scheduled to be built as
> written.
>
> Also note that the "Current State Analysis" was already stale when the plan
> was written: it describes search as pure vector similarity with
> post-filtering, but the shipped system is a hybrid of a BM25 (FTS5) arm and a
> vector arm fused by reciprocal rank fusion with a recency factor. The plan's
> "Hybrid Search" phase therefore proposes inventing something that already
> existed, and its success metric baselines against the wrong system. The
> `exp(-d/30)` recency formula below has a true half-life of about 20.8 days,
> not 30. Kept for the design rationale and as a record of what was dropped.

## Context

Evolve memo-mcp from pure vector search toward GraphRAG (Graph-Retrieval Augmented Generation) capabilities to handle:
- Scale: 10,000+ journal entries
- Second-brain features: automatic concept clustering, pattern detection
- Cross-project insights: relationships between concepts across different JOURNAL_TOKENs

**Current architecture:** Pure vector similarity search via sqlite-vec (KNN on 384-dim embeddings) with post-filtering by section/date.

**Target architecture:** Hybrid retrieval combining vector similarity + graph traversal over an entity-relationship graph extracted from journal entries.

**Approach:** Incremental evolution — add graph layer without breaking existing vector search.

---

## Current State Analysis

### Storage
- **entries** table: `id`, `created_at`, `content` (markdown), `sections` (JSON array)
- **entry_embeddings** vec0 table: `entry_id`, `embedding` float[384]
- One SQLite file per `JOURNAL_TOKEN`: `~/.memo-mcp/{token}.db`

### Search
1. Embed query → 384-dim vector
2. KNN: `SELECT entry_id, distance FROM entry_embeddings WHERE embedding MATCH ? ORDER BY distance LIMIT ?`
3. Fetch 3x limit, post-filter by section/date in Go
4. Return top N with excerpts

### Limitations at Scale
- **Pure vector search degrades** as corpus grows (10k+ entries = many false positives)
- **No concept relationships** — can't answer "show me all entries related to X"
- **No clustering** — can't discover emergent themes across time
- **No cross-project insights** — each token's DB is isolated

---

## GraphRAG Vision

### What Changes
- **Entities extracted** from journal entries (people, projects, concepts, technologies, companies)
- **Relationships tracked** (co-occurrence, mentions, temporal sequence, similarity)
- **Hybrid retrieval**: Vector KNN + graph expansion + reranking
- **Cross-project graph** (optional): separate global graph DB for multi-token insights

### What Stays the Same
- Entry storage (markdown, timestamps, sections) — graph is additive
- Existing MCP tools continue working — no breaking changes
- Pure vector search remains available as fallback
- Token isolation for privacy (cross-project graph opt-in only)

---

## Phase 1: Entity Extraction Pipeline

### Goal
Extract structured entities from journal content without changing existing search.

### Implementation

#### 1.1 Entity Extraction Engine

**Option A: Keyword-based (simple, fast, local)**
- Use TF-IDF or RAKE for keyword extraction
- Go library: `github.com/JesusIslam/tldr` or build custom
- Extract noun phrases via POS tagging
- Pros: Fast, no external dependencies, works offline
- Cons: Lower quality, no semantic understanding

**Option B: LLM-based (high quality, requires API/local LLM)**
- Use Claude API or local Ollama for entity extraction
- Prompt: "Extract entities (people, projects, concepts, technologies) from this text as JSON"
- Pros: High quality, understands context, can classify entity types
- Cons: Requires API calls or local LLM inference (slower, larger)

**Recommended: Start with Option A, evolve to Option B**

#### 1.2 Schema: New Tables

```sql
-- Entity types: person, project, concept, technology, company, location
CREATE TABLE entities (
    id TEXT PRIMARY KEY,           -- UUID
    name TEXT NOT NULL UNIQUE,     -- Canonical name (lowercased, normalized)
    display_name TEXT NOT NULL,    -- Original casing for display
    entity_type TEXT NOT NULL,     -- person|project|concept|technology|company|location
    first_seen INTEGER NOT NULL,   -- Unix milliseconds
    last_seen INTEGER NOT NULL,    -- Unix milliseconds
    mention_count INTEGER DEFAULT 1
);

CREATE INDEX idx_entities_name ON entities(name);
CREATE INDEX idx_entities_type ON entities(entity_type);

-- Entity mentions in entries (join table)
CREATE TABLE entry_entities (
    entry_id TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    mention_count INTEGER DEFAULT 1,  -- How many times in this entry
    PRIMARY KEY (entry_id, entity_id),
    FOREIGN KEY (entry_id) REFERENCES entries(id) ON DELETE CASCADE,
    FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
);

CREATE INDEX idx_entry_entities_entity ON entry_entities(entity_id);
```

#### 1.3 Write Path Changes

**In `journal/journal.go`:**

```go
func (m *Manager) WriteThoughts(ctx context.Context, input ThoughtInput) (string, error) {
    // ... existing code ...
    
    // NEW: Extract entities after embedding (non-blocking)
    if m.entityExtractor != nil {
        go func() {
            entities, err := m.entityExtractor.Extract(content)
            if err != nil {
                log.Printf("entity extraction failed: %v", err)
                return
            }
            if err := m.storeEntities(id, entities); err != nil {
                log.Printf("entity storage failed: %v", err)
            }
        }()
    }
    
    return id, nil
}
```

**New package: `internal/graph/extractor.go`:**

```go
package graph

type Entity struct {
    Name        string
    DisplayName string
    Type        string  // person, project, concept, etc.
}

type Extractor interface {
    Extract(text string) ([]Entity, error)
}

// KeywordExtractor uses TF-IDF/RAKE
type KeywordExtractor struct {
    minScore float64
}

func (e *KeywordExtractor) Extract(text string) ([]Entity, error) {
    // 1. Tokenize
    // 2. Extract noun phrases
    // 3. Score by TF-IDF
    // 4. Classify type (heuristics: all-caps = company, etc.)
    // 5. Return top N
}
```

#### 1.4 MCP Tool: Explore Entities

**New tool: `list_entities`**

```go
type listEntitiesArgs struct {
    EntityType string `json:"entity_type,omitempty"`  // Filter by type
    Limit      int    `json:"limit,omitempty"`        // Default 50
    SortBy     string `json:"sort_by,omitempty"`      // "mention_count" or "last_seen"
}

// Returns: list of entities with mention counts
```

**New tool: `find_entries_by_entity`**

```go
type findByEntityArgs struct {
    EntityName string `json:"entity_name"`
    Limit      int    `json:"limit,omitempty"`
}

// Returns: all entries mentioning this entity, sorted by created_at DESC
```

---

## Phase 2: Relationship Graph

### Goal
Track relationships between entities (co-occurrence, similarity, temporal).

### Implementation

#### 2.1 Relationship Schema

```sql
CREATE TABLE entity_relationships (
    source_entity_id TEXT NOT NULL,
    target_entity_id TEXT NOT NULL,
    relationship_type TEXT NOT NULL,  -- cooccurrence|similar|precedes|mentions
    weight REAL DEFAULT 1.0,           -- Strength (e.g., co-occurrence count)
    first_seen INTEGER NOT NULL,
    last_seen INTEGER NOT NULL,
    PRIMARY KEY (source_entity_id, target_entity_id, relationship_type),
    FOREIGN KEY (source_entity_id) REFERENCES entities(id) ON DELETE CASCADE,
    FOREIGN KEY (target_entity_id) REFERENCES entities(id) ON DELETE CASCADE
);

CREATE INDEX idx_rel_source ON entity_relationships(source_entity_id);
CREATE INDEX idx_rel_target ON entity_relationships(target_entity_id);
```

#### 2.2 Relationship Types

1. **Co-occurrence**: Two entities mentioned in same entry
   - Weight = number of entries where they appear together
   
2. **Similarity**: Entities with similar embedding vectors
   - Compute entity embeddings (average of mention contexts)
   - Store in separate `entity_embeddings` vec0 table
   - Weight = cosine similarity

3. **Temporal sequence**: Entity A often precedes entity B
   - Track ordering in entries
   - Weight = P(B appears after A)

4. **Mentions**: Entry explicitly links two entities
   - Parse markdown links, co-location in sentence
   - Weight = explicit mention count

#### 2.3 Relationship Extraction

**At write time (in background):**

```go
// After extracting entities from an entry:
func (m *Manager) buildRelationships(entryID string, entities []Entity) error {
    // 1. Co-occurrence: all pairs of entities in this entry
    for i := 0; i < len(entities); i++ {
        for j := i+1; j < len(entities); j++ {
            m.upsertRelationship(entities[i].ID, entities[j].ID, "cooccurrence", 1.0)
        }
    }
    
    // 2. Similarity: compute later in batch job
    
    // 3. Temporal: compare to previous entry's entities
    prevEntities := m.getPreviousEntryEntities(entryID)
    for _, prev := range prevEntities {
        for _, curr := range entities {
            if prev.ID != curr.ID {
                m.upsertRelationship(prev.ID, curr.ID, "precedes", 1.0)
            }
        }
    }
}
```

#### 2.4 Entity Embeddings

**New vec0 table:**

```sql
CREATE VIRTUAL TABLE entity_embeddings USING vec0(
    entity_id TEXT PRIMARY KEY,
    embedding float[384]
);
```

**Compute entity embedding:**
- Aggregate all entry content where entity appears
- Embed the concatenated context (or average entry embeddings)
- Store in `entity_embeddings`

**Similarity relationships:**
- Batch job: for each entity pair, compute cosine similarity
- Insert relationship if similarity > threshold (e.g., 0.7)

---

## Phase 3: Graph-Augmented Search

### Goal
Hybrid retrieval: start with vector KNN, expand via graph, rerank.

### Implementation

#### 3.1 New Search Mode: `search_with_graph`

**MCP tool signature:**

```go
type searchGraphArgs struct {
    Query          string   `json:"query"`
    Limit          int      `json:"limit,omitempty"`
    ExpandHops     int      `json:"expand_hops,omitempty"`     // Default 1
    MinEntityScore float64  `json:"min_entity_score,omitempty"` // Default 0.5
    Sections       []string `json:"sections,omitempty"`
}
```

**Algorithm:**

1. **Vector KNN** (existing): Get top 30 entries by embedding similarity
2. **Extract entities** from top results
3. **Graph expansion**: For each entity, find related entities within N hops
4. **Retrieve entries** mentioning expanded entities
5. **Rerank** using combined score:
   - `score = α * vector_similarity + β * graph_relevance + γ * recency`
   - `graph_relevance = sum(relationship_weights to query entities)`
6. **Return** top K results

#### 3.2 Graph Traversal

**New package: `internal/graph/traversal.go`:**

```go
type GraphService struct {
    db *sql.DB
}

// ExpandEntities returns entities reachable from seeds within maxHops
func (g *GraphService) ExpandEntities(seeds []string, maxHops int, minWeight float64) ([]string, error) {
    visited := make(map[string]bool)
    current := seeds
    
    for hop := 0; hop < maxHops; hop++ {
        next := []string{}
        for _, entityID := range current {
            if visited[entityID] {
                continue
            }
            visited[entityID] = true
            
            // Get neighbors
            rows, err := g.db.Query(`
                SELECT target_entity_id, weight 
                FROM entity_relationships 
                WHERE source_entity_id = ? AND weight >= ?
                ORDER BY weight DESC LIMIT 20
            `, entityID, minWeight)
            
            // Add to next frontier
            for rows.Next() {
                var target string
                var weight float64
                rows.Scan(&target, &weight)
                if !visited[target] {
                    next = append(next, target)
                }
            }
        }
        current = next
    }
    
    return keys(visited), nil
}
```

#### 3.3 Scoring Function

```go
func (s *SearchService) hybridScore(
    vectorScore float64,
    graphRelevance float64,
    recencyScore float64,
    alphaVector float64,   // Weight for vector similarity
    betaGraph float64,     // Weight for graph relevance
    gammaRecency float64,  // Weight for recency
) float64 {
    return alphaVector*vectorScore + betaGraph*graphRelevance + gammaRecency*recencyScore
}

// Recency score: exponential decay
func recencyScore(createdAt int64) float64 {
    daysSince := float64(time.Now().UnixMilli()-createdAt) / (1000.0 * 86400.0)
    return math.Exp(-daysSince / 30.0)  // Half-life of 30 days
}
```

---

## Phase 4: Cross-Project Insights

### Goal
Connect insights across different `JOURNAL_TOKEN` databases for pattern detection.

### Implementation

#### 4.1 Global Graph Database

**New database: `~/.memo-mcp/global-graph.db`**

Contains:
- All entities from all tokens (with token annotation)
- Cross-token relationships (similarity, mentions)
- No entry content (privacy) — only entity/relationship metadata

**Schema:**

```sql
CREATE TABLE global_entities (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    display_name TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    source_tokens TEXT NOT NULL,  -- JSON array of tokens where this appears
    total_mentions INTEGER DEFAULT 0
);

CREATE TABLE global_relationships (
    source_entity_id TEXT NOT NULL,
    target_entity_id TEXT NOT NULL,
    relationship_type TEXT NOT NULL,
    weight REAL DEFAULT 1.0,
    source_tokens TEXT NOT NULL,  -- JSON array of tokens contributing to this edge
    PRIMARY KEY (source_entity_id, target_entity_id, relationship_type)
);
```

#### 4.2 Sync to Global Graph

**Opt-in via env var:** `MEMO_ENABLE_GLOBAL_GRAPH=1`

**Background sync:**
- After each entry write, sync entities to global DB
- Merge entities by normalized name
- Aggregate relationships across tokens

#### 4.3 New MCP Tool: `find_patterns`

```go
type findPatternsArgs struct {
    EntityType  string `json:"entity_type,omitempty"`  // Focus on concepts, technologies, etc.
    MinTokens   int    `json:"min_tokens,omitempty"`   // Entities appearing in N+ tokens
    MinMentions int    `json:"min_mentions,omitempty"` // Total mentions threshold
}

// Returns: clustered entity groups with cross-project frequency
```

Example output:
```
Pattern: "TypeScript frustration"
  - Appears in 3 projects (tokens: frontend-work, personal, learning)
  - 47 total mentions
  - Related entities: debugging, type-errors, eslint
  - Temporal trend: increasing mentions over last 3 months
```

---

## Implementation Roadmap

### Phase 1: Foundation (2-3 weeks)
- [ ] Entity extraction (keyword-based)
- [ ] `entities` + `entry_entities` tables
- [ ] Background extraction in write path
- [ ] MCP tools: `list_entities`, `find_entries_by_entity`
- [ ] Tests for entity storage/retrieval

### Phase 2: Relationships (2 weeks)
- [ ] `entity_relationships` table
- [ ] Co-occurrence relationship extraction
- [ ] Entity embeddings + similarity relationships
- [ ] Graph traversal utilities
- [ ] MCP tool: `explore_entity_graph`

### Phase 3: Hybrid Search (2 weeks)
- [ ] Graph-augmented search algorithm
- [ ] Scoring function (vector + graph + recency)
- [ ] MCP tool: `search_with_graph`
- [ ] A/B comparison: vector-only vs. hybrid
- [ ] Performance optimization (graph query caching)

### Phase 4: Cross-Project (1-2 weeks)
- [ ] Global graph database
- [ ] Sync pipeline (opt-in)
- [ ] MCP tool: `find_patterns`
- [ ] Privacy controls (exclude sensitive tokens)

### Phase 5: Advanced (ongoing)
- [ ] LLM-based entity extraction (upgrade from keywords)
- [ ] Automatic concept clustering (K-means on entity embeddings)
- [ ] Temporal pattern detection (trending entities)
- [ ] Export graph to Neo4j/visualization tools

**Total: ~8-10 weeks for Phases 1-4**

---

## Key Design Decisions

### 1. Why SQLite for Graph Storage?

**Pros:**
- No new dependencies (already using SQLite)
- Transactional integrity
- Good enough for 10k-100k entities
- Recursive CTEs support graph queries

**Cons:**
- Not optimized for deep graph traversal (vs. Neo4j)
- Limited to single-machine scale

**Decision:** Start with SQLite. If graph grows to 1M+ nodes, migrate to dedicated graph DB.

### 2. Why Keyword Extraction First?

**Pros:**
- No external dependencies
- Fast (milliseconds)
- Works offline
- Deterministic

**Cons:**
- Lower quality than LLM
- Misses context

**Decision:** Ship Phase 1 with keywords, evolve to LLM in Phase 5. Quality tradeoff for speed.

### 3. Why Opt-In Global Graph?

**Pros:**
- Privacy by default
- User controls cross-project visibility
- Smaller attack surface

**Cons:**
- Feature not available by default
- Requires explicit config

**Decision:** Privacy > convenience. Global graph is opt-in via `MEMO_ENABLE_GLOBAL_GRAPH=1`.

### 4. Why Hybrid Scoring Weights Configurable?

Different use cases need different balances:
- **Recency-focused** (learning journal): high γ
- **Relevance-focused** (research): high α (vector)
- **Discovery-focused** (second brain): high β (graph)

**Decision:** Make α, β, γ MCP tool parameters with sensible defaults.

---

## Verification Plan

### Phase 1: Entity Extraction
- Write 100 test entries with known entities
- Verify entity table populated correctly
- Check `list_entities` returns expected counts
- Check `find_entries_by_entity` retrieves all mentions

### Phase 2: Relationships
- Write entries with overlapping entities
- Verify co-occurrence relationships created
- Check entity embeddings computed
- Verify similarity relationships above threshold

### Phase 3: Hybrid Search
- Compare search_journal (pure vector) vs. search_with_graph (hybrid)
- Measure recall@10 on hand-labeled test set
- Verify graph expansion finds relevant-but-distant entries
- Benchmark query latency (target: <500ms for 10k entries)

### Phase 4: Cross-Project
- Create 3 tokens with overlapping entities
- Enable global graph
- Verify `find_patterns` detects cross-project entities
- Check privacy: global graph has no entry content

---

## Migration Path (Existing Users)

### Zero Downtime
- All new tables added via schema migrations
- Existing search tools unchanged (vector-only)
- New tools additive (search_with_graph, list_entities)

### Backfill Entities
```bash
# CLI tool: memo-mcp backfill-entities --token my-project
# Reads all entries, extracts entities, populates tables
```

### Feature Flags
```go
// In search.go:
if s.graphEnabled {
    return s.searchWithGraph(ctx, query, opts)
} else {
    return s.searchVectorOnly(ctx, query, opts)
}
```

Enable via: `MEMO_ENABLE_GRAPH=1` env var

---

## Files to Modify/Create

### New Packages
- `internal/graph/extractor.go` — Entity extraction interface + keyword impl
- `internal/graph/traversal.go` — Graph traversal utilities
- `internal/graph/schema.go` — Schema creation for graph tables
- `internal/graph/sync.go` — Cross-project sync (Phase 4)

### Modified Packages
- `internal/journal/journal.go` — Add entity extraction to write path
- `internal/search/search.go` — Add `searchWithGraph` method
- `internal/server/server.go` — Register new MCP tools

### New Tools (MCP)
- `list_entities` — Browse entity catalog
- `find_entries_by_entity` — Retrieve all entries mentioning entity
- `explore_entity_graph` — Visualize entity relationships
- `search_with_graph` — Hybrid search (vector + graph)
- `find_patterns` — Cross-project pattern detection (Phase 4)

### Tests
- `internal/graph/extractor_test.go`
- `internal/graph/traversal_test.go`
- Integration test: write → extract → search

---

## Success Metrics

### Quantitative
- **Recall improvement**: Hybrid search finds 20%+ more relevant entries than vector-only
- **Latency**: Graph-augmented search completes in <500ms for 10k entries
- **Entity extraction accuracy**: >70% precision on hand-labeled test set
- **Cross-project insights**: Detect patterns invisible in single-token view

### Qualitative
- Users report discovering connections they didn't know existed
- "Second brain" feel: system surfaces related context proactively
- Claude leverages entity graph to build richer mental models

---

## Open Questions for User

1. **Entity extraction quality vs. speed tradeoff**:
   - Option A: Keyword-based (fast, offline, lower quality)
   - Option B: LLM-based (slow, requires API, high quality)
   - Which to prioritize in Phase 1?

2. **Cross-project graph privacy**:
   - Should global graph be opt-in (current plan) or opt-out?
   - Should there be per-token privacy controls (e.g., mark token as "private")?

3. **Relationship types to prioritize**:
   - Current plan: co-occurrence, similarity, temporal
   - Missing: explicit user-defined relationships (e.g., "X is a prerequisite for Y")
   - Should we add manual relationship editing?

4. **Scoring weights default**:
   - Suggested: α=0.5 (vector), β=0.3 (graph), γ=0.2 (recency)
   - What's your typical use case? (recent journal, long-term research, mixed)
