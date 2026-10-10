# Knowledge bases and MEMORS_HOME

## Where files go

| Path | Contents | Created with |
|---|---|---|
| `$MEMORS_HOME` (default `~/.memors-mcp`) | Base directory | `0700` |
| `$MEMORS_HOME/kb/<name>.db` | One knowledge base (plus `-wal` and `-shm` while open) | `0600` |
| `$MEMORS_HOME/profiles.json` | Optional ranking-profile overrides | You |
| `~/.cache/memors-mcp/models/` | Downloaded embedding and reranker models, one directory each plus a `.ok` marker | `0755` (public model files) |

`<name>` comes from `MEMORS_KB`, default `default`. It must match `^[A-Za-z0-9._-]{1,64}$` and must
not be `.` or `..`. The model cache location is fixed and shared by every knowledge base and
process of the same user.

## One KB per project

Files are the isolation boundary, so the usual pattern is one `MEMORS_KB` per project or client:

```bash
cd ~/src/payments  && claude mcp add memors --env MEMORS_KB=payments  -- ~/.local/bin/memors-mcp
cd ~/src/analytics && claude mcp add memors --env MEMORS_KB=analytics -- ~/.local/bin/memors-mcp
```

Claude Code's default local scope ties each entry to its project directory, so the same server
name `memors` opens a different file in each project.

## Namespaces within a KB

Use namespaces for topics that may be searched together: `docs`, `decisions`, `grpc`. Writes
default to the namespace `default`. Searches span all namespaces unless scoped. Namespace
counts are visible in `memors-mcp status` and as the `memors_kb_namespace_documents` metric.

## Separate homes

`MEMORS_HOME` moves everything except the model cache. Use it to keep test data apart, run CI
against a throwaway directory, or put knowledge bases on an encrypted volume:

```bash
MEMORS_HOME=/Volumes/secure/memors MEMORS_KB=client-a memors-mcp status
```

## Sharing between processes

Several processes may open the same file. WAL mode lets readers proceed during writes, and
writers wait up to 5 seconds for each other. Each process serialises its own writes over one
connection. Do not put knowledge bases on network file systems such as NFS or SMB: SQLite's
locking is not reliable there.

## Older layouts

Before the rename to memors-mcp the base directory was `~/.memo-mcp`, and memors-mcp does not
look there. Move it once with `mv ~/.memo-mcp ~/.memors-mcp` (see
[Upgrading from memo-mcp](upgrading.md#upgrading-from-memo-mcp)); the files inside need no
conversion. Files from the pre-1.0 journal are not opened or migrated.
