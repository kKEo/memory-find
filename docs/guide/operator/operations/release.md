# Release engineering

For maintainers and for operators who build their own releases.

## Pipelines

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | Push, pull request | gofmt, `go vet`, tests under the race detector (these include the retrieval eval gate and the `tools/list` golden), a pure-Go build and smoke run on Linux and macOS (Windows is currently disabled in the test matrix), golangci-lint, and a GoReleaser snapshot that must produce five archives |
| `nightly.yml` | Daily at 03:17 UTC | The eval with the hash embedder and `granite-small-r2`; uploads the report as an artifact |
| `release.yml` | Tag `v*` | Tests, then GoReleaser (five archives, `checksums.txt`), then publishes `server.json` to the MCP registry with GitHub OIDC (no stored secret) |
| `docs.yml` | Push to `docs/guide/**`, tags, manual | Builds these guides with mdBook, runs `scripts/docs-check.sh`, deploys to GitHub Pages |

## Gates a change must pass

- **Eval gate**: no single query may drop a rank band, and no category mean may fall by more
  than 0.02 against `internal/eval/testdata/baseline.json`.
- **Tool-surface golden**: `internal/server/testdata/tools.golden.json` must match. Tool names,
  parameters and descriptions are the 1.x contract.
- **Docs check**: every `MEMO_*` variable, command and metric in the code must appear in this
  guide.

## Cutting a release

```bash
make check                       # lint + race tests
make eval                        # retrieval against the baseline
make snapshot                    # local GoReleaser dry run into dist/
# write docs/eval/vX.Y.Z.md and the CHANGELOG entry
git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z
```

The release notes footer points at `docs/eval/vX.Y.Z.md`. Every tag should have one.

## Versioning

Semantic versioning over the 1.x contract: tool names and parameters, `memo://` addresses,
explain field names and the export front matter. Schema migrations are additive and are
listed in [Upgrading and migrations](../install/upgrading.md).
