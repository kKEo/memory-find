# S7 — Can `promote` ask the human through elicitation?

**Question.** When an agent calls `promote`, can the server put a question in front of the
human (the excerpt, where it came from, the target trust level) and act only on an accepted
answer, over stdio, on protocol 2026-07-28? What happens on a client without the capability?

**Run.** 2026-10-02, go-sdk v1.8.0, in-memory transports with an SDK client whose
`ElicitationHandler` accepts, declines, or is absent (`internal/server/surface_test.go`).

**What we learned.**

| Check | Result |
|---|---|
| `ServerSession.Elicit` from inside a tool handler | **refused** on 2026-07-28: `"elicitation/create" cannot be sent while serving a request … return an InputRequests map instead (SEP-2322)` |
| Multi round-trip shape: handler returns `CallToolResult{InputRequests: {"confirm": &ElicitParams{…}}, RequestState}` with no content | works; the SDK marks the result `input_required`, the client's handler runs, the call is retried with `InputResponses["confirm"]` as `*ElicitResult` |
| Accept with `{"confirm": true}` | trust raised, audit channel `elicitation`, text says "confirmed by the human" |
| Decline / cancel | not applied, result carries the CLI command and the reason |
| Client without the capability (`InitializeParams.Capabilities.Elicitation == nil`) | no dialog attempted; result carries the CLI command |
| Older client that lacks multi round-trip | the SDK's server middleware fulfils the input request with a plain `elicitation/create` and re-invokes the handler, so the same code path serves both |
| Request state | `promote:<uri>:<to>`; an answer echoed back with other arguments is treated as a fresh question. Plain text is enough on a one-user stdio server; an HTTP server would have to sign it |

**Decision.** `promote` is wired to the multi round-trip request; the fallback is the CLI
command. The model never sees the dialog: the SDK routes `elicitation/create` to the client's
handler, not to the tool result.

**Open.** A live check in Claude Code (does the dialog render; does the client resolve resource
templates such as `memo://chunk/{id}`) has not been run on this machine. Until it is, the
README caveat stands: a hook that auto-accepts elicitation removes the protection.
