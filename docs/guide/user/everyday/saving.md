# Saving documents and notes

## What you can save

- **Documents**: design notes, guides, meeting notes, documentation pages Claude has read for
  you, or any markdown or text file.
- **Notes**: a few sentences about something worth keeping.
- **Code explanations**: how a module works, and why it was written that way.
- **Conversations**: a summary of a discussion and what was decided.

## How to ask

> Save this to memors as a note: the staging database is reset every Sunday night.

> Read the file `docs/runbook.md` and save it to memors.

> Fetch the gRPC Go documentation page on deadlines and save it to memors, as version 1.64.

Claude replies with an address such as `memo://doc/01a1…`. You do not need to remember it.
Claude and the browser view use it to point at that exact document.

## Good habits

- **Say where it came from.** When Claude saves a web page, it records the web address. When
  you dictate a note, it records that you said it. Later, every answer shows this.
- **Say which version.** For documentation about a tool or library, mention the version. Later
  questions about that version then get answers for that version, and not for an older one.
- **Add one line of context** to a long document, such as "This is the 2026 security policy
  for the payments team." It helps the right passages surface.

## Saving the same thing twice

Saving an identical document again does nothing, so there are no duplicates. Saving a changed
version keeps both: the new one becomes current, and the old one stays in its history. You
can always see what a document said on an earlier date.

## Saving from the terminal

If you prefer, you can save files without Claude, for example a whole folder of notes:

```bash
memors-mcp ingest ~/notes/project-x
```

Things you save yourself this way are marked as *yours*. Things Claude saves are marked as
*Claude's*. [Trust](../trust.md) explains why that matters.
