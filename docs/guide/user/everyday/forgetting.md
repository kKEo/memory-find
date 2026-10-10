# Forgetting things

Sometimes a note is wrong, out of date, or should never have been saved.

## Asking Claude to forget

> Forget the old onboarding note in memors; it describes the previous tooling.

Claude asks for, or supplies, a short reason. The note then disappears from searches.

Its address keeps working, though. Anyone who follows an old link to it sees that it was
forgotten, when, and why, instead of a dead end. That keeps the record honest.

## What Claude may and may not forget

Claude can only forget things Claude saved. Things you saved yourself, or marked as checked,
can only be removed by you. This prevents a mistaken or misled assistant from deleting your
own records.

To remove something yourself, use the terminal with the item's address:

```bash
memors-mcp forget memo://doc/01a1... --reason "replaced by the 2026 policy"
```

## Removing text completely

Forgetting hides an item and keeps its text in history. If text must be erased, for example a
password pasted by mistake, add `--redact`:

```bash
memors-mcp forget memo://doc/01a1... --reason "contained a secret" --redact
```

The record that something was removed stays. The text itself is gone.
