package eval

import (
	"fmt"
	"strings"

	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
)

// longFiller generates repeated, topic-flavored filler text long enough
// (in combination across a few sections) to push a fixture entry's total
// content past the ~1500-rune interim embedding cap (see
// journal.truncateForEmbedding). It exists to build fixtures that
// reproduce the project's headline bug on purpose: content placed after
// the cap is invisible to the vector arm but still fully indexed by FTS,
// so these fixtures measure exactly that gap.
func longFiller(topic string, repeats int) string {
	sentence := fmt.Sprintf("General debugging notes about %s, including timelines, participants, and follow-up actions that were discussed at length. ", topic)
	return strings.Repeat(sentence, repeats)
}

// Corpus returns the standard fixture corpus used by the retrieval eval
// harness: a mix of thematic clusters (sharing real vocabulary, so a
// lexical embedder like embedding.HashEmbedder can meaningfully relate
// them), long entries that reproduce the embedding-truncation gap,
// near-duplicate stem variants that reproduce the missing-porter-stemmer
// gap, a section-skewed block for filter-pushdown testing, a
// knowledge-update pair for future Phase 3 reuse, and pure-noise "world
// knowledge" entries that serve as negative controls.
func Corpus() []FixtureEntry {
	entries := []FixtureEntry{
		// --- Auth / session cluster ---
		{Key: "auth-1", Input: journal.ThoughtInput{ProjectNotes: "The authentication service issues JWT tokens with a 24 hour expiry. Session refresh happens automatically in the middleware layer before the token expires."}},
		{Key: "auth-2", Input: journal.ThoughtInput{ProjectNotes: "Session tokens were leaking across requests because the middleware cached the auth context per connection instead of per request. Fixed by scoping the session lookup to the request context."}},
		{Key: "auth-3", Input: journal.ThoughtInput{TechnicalInsights: "Learned that JWT refresh tokens should be rotated on every use to limit the blast radius of a stolen refresh token. Implemented rotation in the auth service this week."}},
		{Key: "auth-4", Input: journal.ThoughtInput{Reflections: "Spent the afternoon debugging why users were getting logged out randomly. Turned out the session expiry check compared UTC and local time inconsistently."}},
		{Key: "auth-5", Input: journal.ThoughtInput{Observations: "Auth service p99 latency spiked after adding the session refresh check on every request."}},

		// --- Database / Postgres cluster ---
		{Key: "db-1", Input: journal.ThoughtInput{ProjectNotes: "PostgreSQL query planner picked a sequential scan on the orders table instead of using the index on customer_id. Added an explicit index hint and it now uses the index."}},
		{Key: "db-2", Input: journal.ThoughtInput{ProjectNotes: "Database migration to add a NOT NULL column locked the users table for twenty minutes in staging because Postgres rewrites the whole table for that kind of migration."}},
		{Key: "db-3", Input: journal.ThoughtInput{TechnicalInsights: "Learned that adding a column with a non-constant DEFAULT in Postgres before version 11 rewrites the entire table; a constant default doesn't."}},
		{Key: "db-4", Input: journal.ThoughtInput{Reflections: "Frustrated with how long the nightly database backup takes now that the orders table has grown past ten million rows."}},
		{Key: "db-5", Input: journal.ThoughtInput{Observations: "Postgres connection pool exhausted under load; increased max_connections and added pgbouncer in front."}},

		// --- Frontend / React cluster ---
		{Key: "fe-1", Input: journal.ThoughtInput{ProjectNotes: "React component was re-rendering on every keystroke because the parent passed a new callback function on each render. Wrapped it in useCallback."}},
		{Key: "fe-2", Input: journal.ThoughtInput{ProjectNotes: "Frontend bundle size ballooned after adding the charting library. Investigating tree-shaking and code-splitting to bring it back down."}},
		{Key: "fe-3", Input: journal.ThoughtInput{TechnicalInsights: "Virtual DOM diffing gets expensive with large lists; switched to windowing so only visible rows are rendered."}},
		{Key: "fe-4", Input: journal.ThoughtInput{Observations: "React rendering is noticeably slow on older mobile devices when the product list has more than a few hundred items."}},

		// --- gRPC / RPC cluster ---
		{Key: "rpc-1", Input: journal.ThoughtInput{TechnicalInsights: "Implemented a gRPC unary interceptor for authentication so every service method gets the auth check for free instead of duplicating it."}},
		{Key: "rpc-2", Input: journal.ThoughtInput{ProjectNotes: "gRPC interceptor chain order matters: the logging interceptor needs to run before the auth interceptor rejects a request, or failed auth attempts never get logged."}},
		{Key: "rpc-3", Input: journal.ThoughtInput{Observations: "Considered switching an internal REST endpoint to a binary RPC protocol for lower latency between services."}},

		// --- Testing cluster ---
		{Key: "test-1", Input: journal.ThoughtInput{TechnicalInsights: "Dependency injection makes mocking the embedder trivial in tests; the real embedding backend never needs to load in CI."}},
		{Key: "test-2", Input: journal.ThoughtInput{ProjectNotes: "The integration test suite was flaky because it shared one SQLite connection across parallel tests. Fixed by giving each test its own temp file database."}},
		{Key: "test-3", Input: journal.ThoughtInput{Reflections: "Looking at the test coverage report, most of the uncovered lines are error paths nobody has ever hit in production."}},

		// --- Deployment / infra cluster ---
		{Key: "infra-1", Input: journal.ThoughtInput{ProjectNotes: "Kubernetes rolled out the new deployment before the database migration finished, so a handful of requests hit the old schema. Added a migration-complete health check gate."}},
		{Key: "infra-2", Input: journal.ThoughtInput{TechnicalInsights: "Docker image size dropped from 900MB to 40MB after switching the base image to a distroless one and removing the build toolchain from the final stage."}},
		{Key: "infra-3", Input: journal.ThoughtInput{Observations: "Rolled back the Friday deploy after error rates spiked; turned out to be an unrelated DNS issue, not the deploy itself."}},

		// --- Personal / emotional cluster ---
		{Key: "mood-1", Input: journal.ThoughtInput{Reflections: "Feeling overwhelmed by the sprint deadline, there are too many pull requests to review this week."}},
		{Key: "mood-2", Input: journal.ThoughtInput{Reflections: "Burned out from context switching between three different projects all week."}},
		{Key: "mood-3", Input: journal.ThoughtInput{Reflections: "Anxious about the on-call rotation starting Monday; the runbooks feel out of date."}},
		{Key: "mood-4", Input: journal.ThoughtInput{UserContext: "The user mentioned they've been stressed about a tight launch deadline and prefers concise status updates over long explanations right now."}},
		{Key: "mood-5", Input: journal.ThoughtInput{Observations: "Took a long walk at lunch today, felt calmer afterward."}},

		// --- World knowledge cluster: pure noise, negative controls ---
		{Key: "wk-1", Input: journal.ThoughtInput{WorldKnowledge: "The Great Barrier Reef is the largest living structure on Earth, visible even from space."}},
		{Key: "wk-2", Input: journal.ThoughtInput{WorldKnowledge: "Octopuses have three hearts and blue blood, and two of the hearts stop beating when they swim."}},
		{Key: "wk-3", Input: journal.ThoughtInput{WorldKnowledge: "The Eiffel Tower grows about 15 centimeters taller in summer due to thermal expansion of the iron."}},
		{Key: "wk-4", Input: journal.ThoughtInput{WorldKnowledge: "Honey never spoils; archaeologists have found pots of honey in ancient Egyptian tombs that are still edible."}},
		{Key: "wk-5", Input: journal.ThoughtInput{WorldKnowledge: "A single bolt of lightning contains enough energy to toast about one hundred thousand slices of bread."}},

		// --- Stem-variant cluster: same root word, different surface
		// form. Neither FTS5 (no porter tokenizer yet) nor
		// embedding.HashEmbedder (exact-token lexical hashing, no
		// stemming) can currently bridge "review"/"reviewing"/"reviewer"
		// to each other. This is a known, currently-accepted gap that
		// Phase 2's porter-stemmed FTS index should close — see the
		// corresponding query's comment below.
		{Key: "stem-1", Input: journal.ThoughtInput{ProjectNotes: "Finished reviewing the pull request for the payment service refactor."}},
		{Key: "stem-2", Input: journal.ThoughtInput{Observations: "The code review took longer than expected because of merge conflicts."}},
		{Key: "stem-3", Input: journal.ThoughtInput{TechnicalInsights: "Reviewers should focus on correctness first, style nits second."}},

		// --- Near-duplicate / paraphrase pair: same fact, different
		// wording, both should be retrievable for the same query.
		{Key: "dup-1", Input: journal.ThoughtInput{ProjectNotes: "The onboarding flow drops about 40% of new users at the email verification step."}},
		{Key: "dup-2", Input: journal.ThoughtInput{ProjectNotes: "Roughly four in ten new signups abandon onboarding right at email verification."}},

		// --- Knowledge-update pair: same fact, superseded by a later
		// entry. There is no supersede mechanism yet (Phase 3 of the
		// project roadmap), so for now these are just two ordinary
		// entries with a recency gap between them; the query below checks
		// that recency alone gives some preference to the newer one.
		{Key: "kb-old", AgeDays: 180, Input: journal.ThoughtInput{ProjectNotes: "The public API rate limit is 100 requests per minute per API key."}},
		{Key: "kb-new", AgeDays: 1, Input: journal.ThoughtInput{ProjectNotes: "Updated the public API rate limit to 500 requests per minute per API key after the partner integration needed higher throughput."}},

		// --- Long entries (>6000 chars) with a distinctive marker in the
		// LAST formatted section (world_knowledge — see
		// journal.formatMarkdown's fixed section order). Filler content in
		// the earlier sections pushes the marker well past the ~1500-rune
		// embedding cap, so it should be findable via keyword search but
		// not via vector similarity — that split is exactly what these
		// fixtures are for measuring.
		{Key: "long-1", Input: journal.ThoughtInput{
			Reflections:       longFiller("the July deployment incident", 20),
			ProjectNotes:      longFiller("the rollback procedure", 20),
			TechnicalInsights: longFiller("the on-call response timeline", 20),
			WorldKnowledge:    "The rare deployment incident tracking code for this event is INCIDENT-7734-ZEBRA, filed for follow-up during the July migration window.",
		}},
		{Key: "long-2", Input: journal.ThoughtInput{
			Reflections:       longFiller("the websocket disconnect investigation", 20),
			ProjectNotes:      longFiller("the load balancer configuration", 20),
			TechnicalInsights: longFiller("the client reconnect logic", 20),
			WorldKnowledge:    "The support ticket about intermittent websocket disconnects was tracked under ticket WEBSOCK-4521-FALCON for engineering follow-up.",
		}},
		{Key: "long-3", Input: journal.ThoughtInput{
			Reflections:       longFiller("the billing reconciliation process", 20),
			ProjectNotes:      longFiller("the invoice generation pipeline", 20),
			TechnicalInsights: longFiller("the payment provider webhook handling", 20),
			WorldKnowledge:    "The customer who reported the billing discrepancy was flagged internally as ACCOUNT-9910-COMET for account-team follow-up.",
		}},
	}

	// Section-skew block: many "reflections" decoys sharing a generic
	// theme, plus one true target in "world_knowledge" — for testing that
	// a section-filtered search finds the target regardless of how many
	// same-topic decoys exist in a different section (see
	// search.filteredEntryIDs and the corresponding regression test in
	// internal/search).
	for i := 1; i <= 35; i++ {
		entries = append(entries, FixtureEntry{
			Key: fmt.Sprintf("standup-decoy-%d", i),
			Input: journal.ThoughtInput{
				Reflections: fmt.Sprintf("Day %d: attended the daily standup meeting, nothing major to report.", i),
			},
		})
	}
	entries = append(entries, FixtureEntry{
		Key:   "standup-target",
		Input: journal.ThoughtInput{WorldKnowledge: "The daily standup meeting format was originally borrowed from Scrum, where it's called the daily scrum."},
	})

	return entries
}

// Queries returns the standard labelled query set evaluated against
// Corpus(). Each query's Relevant/Irrelevant sets reflect a human
// curator's honest judgment of what should match — not a prediction of
// what the current system will find. The gap between the two is exactly
// what the eval report measures, and what testdata/baseline.json records
// as the currently-accepted starting point.
func Queries() []Query {
	return []Query{
		// --- Plain lexical matches: exact shared vocabulary ---
		{ID: "auth-jwt-expiry", Query: "JWT token expiry session refresh", Relevant: []string{"auth-1", "auth-3"}, Irrelevant: []string{"db-1", "fe-1"}},
		{ID: "auth-session-bug", Query: "session tokens leaking across requests", Relevant: []string{"auth-2"}, Irrelevant: []string{"auth-1"}},
		{ID: "postgres-index", Query: "postgres query planner sequential scan index", Relevant: []string{"db-1"}, Irrelevant: []string{"db-2"}},
		{ID: "postgres-migration-lock", Query: "database migration locked table", Relevant: []string{"db-2", "db-3"}, Irrelevant: []string{"db-1"}},
		{ID: "react-rerender", Query: "React component re-rendering callback", Relevant: []string{"fe-1"}, Irrelevant: []string{"fe-3"}},
		{ID: "grpc-interceptor", Query: "gRPC interceptor authentication", Relevant: []string{"rpc-1", "rpc-2"}, Irrelevant: []string{"auth-1"}},
		{ID: "docker-image-size", Query: "docker image size distroless base image", Relevant: []string{"infra-2"}},
		{ID: "kubernetes-rollout", Query: "kubernetes deployment rollout migration", Relevant: []string{"infra-1"}},

		// --- Partial-lexical / "semantic-ish": shares some but not all
		// vocabulary, exercising vector recall under a lexical embedder.
		{ID: "auth-random-logout", Query: "users logged out unexpectedly time zone bug", Relevant: []string{"auth-4"}},
		{ID: "db-slow-backup", Query: "backup taking a long time large table", Relevant: []string{"db-4"}},
		{ID: "frontend-slow-mobile", Query: "frontend performance slow on mobile devices", Relevant: []string{"fe-4", "fe-2"}},
		{ID: "flaky-tests", Query: "flaky integration tests shared database connection", Relevant: []string{"test-2"}},
		{ID: "onboarding-drop-off", Query: "new users dropping off during signup verification", Relevant: []string{"dup-1", "dup-2"}},

		// --- Emotional / reflections cluster ---
		{ID: "mood-deadline-stress", Query: "overwhelmed sprint deadline pull requests", Relevant: []string{"mood-1"}, Irrelevant: []string{"mood-5"}},
		{ID: "mood-burnout", Query: "burned out context switching between projects", Relevant: []string{"mood-2"}},
		{ID: "mood-oncall-anxiety", Query: "anxious about on-call rotation runbooks", Relevant: []string{"mood-3"}},
		{ID: "user-context-deadline", Query: "user prefers concise updates stressed about deadline", Relevant: []string{"mood-4"}},

		// --- World-knowledge / negative controls: no technical-cluster
		// vocabulary overlap at all, so a technical query should not pull
		// these to the top.
		{ID: "wk-negative-control-auth", Query: "JWT token expiry session refresh middleware", Relevant: []string{"auth-1", "auth-3"}, Irrelevant: []string{"wk-1", "wk-2", "wk-3"}},
		{ID: "wk-negative-control-db", Query: "postgres index sequential scan query planner", Relevant: []string{"db-1"}, Irrelevant: []string{"wk-4", "wk-5"}},

		// --- Pure no-match: a domain entirely absent from the corpus.
		// There is no min-similarity threshold yet (that's Phase 2), so
		// this can't assert "zero results" — instead it declares no
		// Relevant entries and tracks whether specific unrelated entries
		// get pulled to the top regardless (they shouldn't dominate).
		{ID: "no-match-cooking", Query: "sourdough starter fermentation baking technique", Irrelevant: []string{"auth-1", "db-1", "fe-1"}},
		{ID: "no-match-astronomy", Query: "exoplanet atmospheric spectroscopy telescope", Irrelevant: []string{"rpc-1", "infra-1"}},

		// --- Stem-variant: documents the current gap. stem-1 and stem-3
		// are NOT listed as Relevant even though a human would consider
		// them on-topic, because neither FTS5 (unicode61, no stemmer) nor
		// HashEmbedder (exact-token hashing) can currently bridge
		// "review" to "reviewing"/"reviewers". Once Phase 2 adds a porter
		// FTS tokenizer, this query's Relevant set should be expanded to
		// all three and the baseline updated to expect the improvement.
		{ID: "stemming-gap-review", Query: "review pull request", Relevant: []string{"stem-2"}},

		// --- Section-filtered: many same-section decoys elsewhere, one
		// true target in world_knowledge.
		{ID: "section-filtered-standup", Query: "standup meeting scrum origin", Opts: search.SearchOptions{Sections: []string{"world_knowledge"}}, Relevant: []string{"standup-target"}},

		// --- Date-filtered / recency: two versions of the same fact,
		// only recency (no supersede mechanism yet) distinguishes them.
		{ID: "recency-rate-limit", Query: "public API rate limit requests per minute", Relevant: []string{"kb-new", "kb-old"}},

		// --- Long-entry marker queries: the marker text lives in the
		// LAST formatted section of a >6000-char entry, past the interim
		// embedding truncation cap. Expected (and worth watching in the
		// report): found via keyword match, not via vector similarity.
		{ID: "long-entry-incident-code", Query: "INCIDENT-7734-ZEBRA deployment incident", Relevant: []string{"long-1"}},
		{ID: "long-entry-websocket-ticket", Query: "WEBSOCK-4521-FALCON websocket disconnect ticket", Relevant: []string{"long-2"}},
		{ID: "long-entry-billing-account", Query: "ACCOUNT-9910-COMET billing discrepancy", Relevant: []string{"long-3"}},

		// --- Multi-signal hybrid: shares both an exact keyword and
		// topic-level vocabulary with more than one entry, requiring
		// both arms to cooperate to rank the double-signal entry first.
		{ID: "hybrid-frontend-performance", Query: "slow rendering performance bundle size", Relevant: []string{"fe-2", "fe-4"}, Irrelevant: []string{"fe-1"}},
		{ID: "hybrid-grpc-chain-order", Query: "interceptor chain order logging before auth", Relevant: []string{"rpc-2"}, Irrelevant: []string{"rpc-1"}},
	}
}
