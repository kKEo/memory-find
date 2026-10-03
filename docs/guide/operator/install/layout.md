# Knowledge bases and MEMO_HOME

## Where files go

| Path | Contents | Created with |
|---|---|---|
| `$MEMO_HOME` (default `~/.memo-mcp`) | Base directory | `0700` |
| `$MEMO_HOME/kb/<name>.db` | One knowledge base (plus `-wal` and `-shm` while open) | `0600` |
| `$MEMO_HOME/profiles.json` | Optional ranking-profile overrides | You |
| `~/.cache/memo-mcp/models/` | Downloaded embedding and reranker models, one directory each plus a `.ok` marker | `0755` (public model files) |

`<name>` comes from `MEMO_KB`, default `default`. It must match `^[A-Za-z0-9._-]{1,64}$` and must
not be `.` or `..`. The model cache location is fixed and shared by every knowledge base and
process of the same user.

## One KB per project

Files are the isolation boundary, so the usual pattern is one `MEMO_KB` per project or client:

```bash
cd ~/src/payments  && claude mcp add memo --env MEMO_KB=payments  -- ~/.local/bin/memo-mcp
cd ~/src/analytics && claude mcp add memo --env MEMO_KB=analytics -- ~/.local/bin/memo-mcp
```

Claude Code's default local scope ties each entry to its project directory, so the same server
name `memo` opens a different file in each project.

## Namespaces within a KB

Use namespaces for topics that may be searched together: `docs`, `decisions`, `grpc`. Writes
default to the namespace `default`. Searches span all namespaces unless scoped. Namespace
counts are visible in `memo-mcp status` and as the `memo_kb_namespace_documents` metric.

## Separate homes

`MEMO_HOME` moves everything except the model cache. Use it to keep test data apart, run CI
against a throwaway directory, or put knowledge bases on an encrypted volume:

```bash
MEMO_HOME=/Volumes/secure/memo MEMO_KB=client-a memo-mcp status
```

## Sharing between processes

Several processes may open the same file. WAL mode lets readers proceed during writes, and
writers wait up to 5 seconds for each other. Each process serialises its own writes over one
connection. Do not put knowledge bases on network file systems such as NFS or SMB: SQLite's
locking is not reliable there.

## Legacy variables

`JOURNAL_TOKEN` and `JOURNAL_PATH` are accepted for one more release as aliases of `MEMO_KB`
and `MEMO_HOME`, with a deprecation warning. Files from the pre-1.0 journal are not opened or
migrated.
