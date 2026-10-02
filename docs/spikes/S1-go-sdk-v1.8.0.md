# S1 — Does go-sdk v1.8.0 break anything?

**Question.** Can memo-mcp move from go-sdk v1.6.0 (protocol 2025-11-25) to v1.8.0 (protocol
2026-07-28) without changes to the six existing tools, the in-memory test session, or the
`tools/list` golden file? Does a current client negotiate 2026-07-28 over stdio?

**Run.** 2026-10-02, `go get github.com/modelcontextprotocol/go-sdk@v1.8.0 && go mod tidy`,
then `go build ./... && go test ./... -race`.

**Numbers.**

| Check | Result |
|---|---|
| Build | clean, no source changes needed |
| Tests (all packages, `-race`) | green |
| `tools/list` golden regenerated with `-update` | **no diff** (the SDK did not start serialising annotations we never set) |
| Negotiated protocol version (in-memory transports, SDK client) | `2026-07-28`, asserted by `TestNegotiatesCurrentProtocolVersion` |

**Decision.** Bump to v1.8.0 in P0. New tools in P2 are built directly on v1.8.0. Nothing to fall
back to. Not yet checked (deferred to the phases that need them): `SetCacheable` TTL hints (P5),
elicitation over stdio in a real client (S7, P4/P5), `outputSchema` round trip (P2, covered by the
stubbed `TestStructuredContentValidates` when it activates).
