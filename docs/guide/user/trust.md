# Trust: who vouched for what

Every item in memors-mcp carries a **trust level**. It answers one question: *who has vouched for
this?*

| Level | Meaning | How something gets it |
|---|---|---|
| **Agent** | Claude saved it. It may well be right, but no person has checked it. | Anything Claude saves |
| **User** | A person saved it or confirmed it. | Anything you save from the terminal, or Claude's work you approve |
| **Curated** | Checked and approved as a reference source. | Only by a person, deliberately |

The level appears next to every search result, in the browser view and in what Claude tells
you.

![Three levels from low to high: Agent, where everything Claude saves starts; User, where your terminal saves start; and Curated. Only a person can raise trust, by accepting a dialog or running memors-mcp trust promote; Claude can only ask. A person can lower trust with memors-mcp trust demote.](images/trust-levels.svg)

## Why Claude cannot raise trust by itself

If an assistant could mark its own notes as verified, the label would mean nothing. A
web page with misleading instructions could also persuade it to "verify" something false. So
memors-mcp has one firm rule: **only a person can raise trust.**

Claude can *ask*. When it believes a note deserves more trust, it requests a promotion, and
one of two things happens:

- **Your app shows a dialog.** It contains the passage, where it came from and the level
  requested. Nothing changes unless you press accept.
- **Your app cannot show dialogs.** Claude gives you a short command to run yourself, for
  example:

  ```bash
  memors-mcp trust promote memo://doc/01a1... --to user
  ```

Every change of trust is recorded: who made it, when and why.

> **Warning.** Some setups can be configured to accept every dialog automatically. If you do
> that, the trust labels lose their meaning. Treat everything as *agent* level.

## Lowering trust

If something you trusted turns out to be wrong, lower it:

```bash
memors-mcp trust demote memo://doc/01a1... --to agent
```

## How trust affects answers

Trust does not change how relevant a result is. A note Claude saved can still be the best match.
What trust changes:

- **You can see it.** Every answer shows whether it rests on something a person checked, or only
  on Claude's own notes.
- **You can require it.** Ask Claude to use only items at or above a level:

  > Answer only from items a person has checked.

- **It settles ties.** When two facts disagree, the one with higher trust is preferred, then
  the newer one.
