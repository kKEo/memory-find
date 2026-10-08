# The menu-bar app (macOS)

If you run memo-mcp as one shared server (`memo-mcp serve --http`, see
[Looking inside](looking-inside.md)), **memo-tray** puts it in your menu bar. At a glance you
see which agents are connected right now, you can open the server's live page in a window of
its own, and you can start and stop servers without a terminal.

## Installing it

1. Install memo-mcp itself as usual. memo-tray runs it; it does not replace it.
2. Download `memo-tray_X.Y.Z_darwin_universal.zip` from the
   [releases page](https://github.com/kKEo/memory-find/releases), unzip it and move
   **memo-tray.app** to Applications.
3. Open it. The first time, macOS says it cannot check the developer: the app is not
   notarized. Open **System Settings › Privacy & Security** and click **Open Anyway**. (Or, in a
   terminal: `xattr -dr com.apple.quarantine /Applications/memo-tray.app`.)
4. To have it start when you log in, add it under **System Settings › General › Login Items**.

memo-tray looks for memo-mcp in the usual places (`~/.local/bin`, `/opt/homebrew/bin`,
`/usr/local/bin`, `~/go/bin`). If yours is elsewhere, set `memo_binary` in its settings (below).

## What the menu shows

```
crportal · 127.0.0.1:8765 · running      ▸
    ● claude-code 2.1.4 — search (3s)
    ○ cursor 1.2 — idle 12m
default · stopped                        ▸
Start Server                             ▸
Settings                                 ▸
Quit memo-tray (stops 1 server)
```

- One line per server: its memory's name, its address and its state. The submenu (▸) has
  everything you can do with it.
- Under each server, one line per connected agent:
  - **●** the agent is busy (the call it is running is shown) or connected and active;
  - **○** its connection is open but it has been quiet.
- The number next to the menu-bar icon counts the **●** agents.

An agent disappears from the list when it disconnects. One that stays quiet for more than 30
minutes is dropped by the server and shows up again with its next request.

## Starting and stopping servers

- **Start Server ▸** lists your memories. Pick one and memo-tray starts a server for it on
  `127.0.0.1:8765`, or the next free port. It remembers the address, so the URL you give your
  agents never changes.
- **Copy "claude mcp add" Command** in a server's submenu puts the line that connects Claude
  Code on the clipboard. Paste it in a terminal once.
- **Stop**, **Restart** and **Show Log** are in the same submenu. Servers you started in a
  terminal appear too, and you can stop them from here.
- Quitting memo-tray stops the servers it started. To keep them running, set
  `"stop_on_quit": false`.

## The stats window

**Open Stats Window** shows the server's **Live** page in a window of its own: connected
clients, calls in flight, per-tool timings and ingest progress, refreshed every 2 seconds. The
other pages (search, documents, facts) work in the window too. Links that leave the server open
in your normal browser.

If the server asks for a token, the window logs in by itself. There is nothing to paste.

## Settings

**Settings › Edit Config…** opens `~/.memo-mcp/tray.json`:

```json
{
  "servers": [
    { "kb": "crportal", "addr": "127.0.0.1:8765", "autostart": true },
    { "kb": "work", "addr": "127.0.0.1:8766", "auth": "token" }
  ],
  "env": { "MEMO_MODEL": "potion" },
  "stop_on_quit": true
}
```

- `autostart` starts that server whenever memo-tray starts.
- `auth: "token"` keeps other people on a shared Mac out (see the operator guide).
- `env` passes settings to every server memo-tray starts. Apps opened from the Dock do not see
  your terminal's variables, so put settings such as `MEMO_MODEL` here.

Logs live in `~/.memo-mcp/logs/`: one file per server (`serve-<name>.log`) and `tray.log`.
