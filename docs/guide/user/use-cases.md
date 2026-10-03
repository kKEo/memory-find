# Use cases

## Project memory for a codebase

**The situation.** Every Claude session on your project starts by re-reading the same files
and re-learning the same conventions.

**With memo-mcp.** Save the architecture notes, conventions and "why we did it this way"
explanations once. Claude looks them up when they are relevant. When the code changes, ask
Claude to save the new version. The old one stays as history.

> At the end of a working session: "Save to memo what we learned today about the payment
> retry logic, and why we chose exponential backoff."

## Documentation pinned to the version you use

**The situation.** Libraries change between versions, and assistants mix them up.

**With memo-mcp.** Have Claude fetch and save the documentation for the exact version you use,
labelled with that version. Questions then get answers for your version.

> "Fetch the gRPC Go docs on deadlines for version 1.64 and save them to memo."

## Team decisions and their reasons

**The situation.** Six months later, nobody remembers why a decision was made, and it gets
argued again.

**With memo-mcp.** Save each decision with its reason and date. Record the outcome as a fact.
When it changes, record the new fact as replacing the old one. The history shows when and why.

> "Remember that we chose Postgres over MongoDB on 12 May because we need transactions across
> accounts. Link it to the meeting notes."

## Research notes

**The situation.** You read many sources, and later cannot find which one said what.

**With memo-mcp.** Have Claude save each source with its address and a one-line context. Ask
questions across all of them. Every answer names its source, and you can open the exact passage.

## Onboarding a new teammate

**The situation.** A new person spends weeks absorbing knowledge nobody wrote down.

**With memo-mcp.** The team's memory is already there. The newcomer can browse it in the
[browser view](looking-inside.md) or ask Claude questions against it. Trust labels show which
answers a person has checked.

## Support and operations runbooks

**The situation.** During an incident, you need the procedure that is current *now*.

**With memo-mcp.** Save runbooks and record key facts, such as on-call contacts, limits and
endpoints. Facts that change are corrected rather than duplicated, so the current answer is
always the one that comes back.
