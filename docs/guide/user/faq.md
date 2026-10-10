# Questions and troubleshooting

## Claude doesn't seem to see memors

- **Claude Code:** type `/mcp`. `memors` should be listed as connected. If it shows an error,
  the message usually says what is wrong.
- **Claude Desktop:** quit it completely, not just the window, and open it again. Check that
  the path in the settings file is the full path to the program, starting with `/` on a Mac or
  `C:\` on Windows.
- **Shared server** (`memors-mcp serve --http`): check that the terminal running it is still
  open and shows no error, then run `/mcp` in Claude Code to reconnect. The address in
  `claude mcp add` must end in `/mcp` and use the same port the server prints.
- On a Mac, the program may be blocked the first time. See
  [Quick start](quick-start.md), step 1.

## It found nothing

- **It may really not be there.** memors-mcp says "nothing found" instead of guessing. Ask Claude
  to save the answer once it finds it elsewhere.
- **You may be looking in a different memory.** Each memory has a name. If Claude was set up
  with `my-project` and you saved things under another name, they are in a different file. Ask
  Claude "What is in my memors knowledge base?" to see which one it uses.
- **The question may be limited to one shelf or version.** Ask again without the limit.

## Results feel off

- Ask "Why did that come first?" The explanation often shows the cause: an old note, a weak
  match, or the wrong version.
- Open the same search in the [browser view](looking-inside.md) and look at the sources.
- Save a better source, or correct the fact. Old, wrong notes can be forgotten; see
  [Forgetting things](everyday/forgetting.md).

## Claude says search is "degraded"

The language model that matches by meaning is not ready. This is normal for a few minutes
after the first start, or after switching models, while it downloads or catches up. Search
still works by matching words. If it lasts, the computer may have no internet access for the
one-time download. Ask whoever set it up, or see the
[operator guide](../operator/operations/troubleshooting.md).

## Is my information sent anywhere?

No. See [Privacy and safety](privacy.md). The only download is the one-time language model.

## Can I move my memory to another computer?

Yes. Your memory is one file:

1. Close Claude on the old computer, so the file is not in use.
2. Copy the file from the `.memors-mcp/kb` folder in your home folder. Its name is the memory
   name followed by `.db`, for example `my-project.db`.
3. Install memors-mcp on the new computer ([Quick start](quick-start.md)), and put the file in
   the same folder there.

The language model downloads again on the new computer the first time.

## How do I back it up?

Close Claude, then copy the same file somewhere safe. To back it up while Claude is open, see
[Backup and restore](../operator/operations/backup.md) in the operator guide.

## Can several people share one memory?

memors-mcp is built for one person on one computer. A team can share by exporting the memory as
documents, or by having one person curate a memory others browse. Simultaneous use over a
network drive is not supported.

## Can I read my memory without Claude?

Yes. Use the [browser view](looking-inside.md). An operator can also export everything as
ordinary documents that open in any editor or in Obsidian.

## Where do I get help?

Open an issue on [GitHub](https://github.com/kKEo/memors/issues). Describe what you asked
and what happened, but do not include anything private.
