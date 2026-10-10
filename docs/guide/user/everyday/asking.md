# Asking questions

## How to ask

Ask the way you would ask a colleague, and mention memors so Claude checks the memory first:

> What does memors say about how we handle failed payments?

> Check memors: which version of the gRPC library do we use, and what changed in it?

Claude searches and reads the most relevant passages. It then answers and names its sources.

## What comes back

Each result carries:

- **Where it came from**: the document title and, for web pages, the original address.
- **Who saved it**: you, Claude, or a checked source. See [Trust](../trust.md).
- **When it was saved**, and whether a newer version exists.
- **How strong the match is**: *strong*, *moderate* or *weak*. A weak match is a hint, not an
  answer, and Claude should treat it that way.

## Asking why

Every search can explain itself. Ask:

> Why did that come up first?

Claude can show which kinds of matching found each result. Some results match your exact
words, some match a name or code identifier exactly, and some match the meaning even when the
words differ. Claude can also show how each result scored. The same explanation is visible in
the browser view. See [Looking inside](../looking-inside.md).

## When nothing is found

If the memory holds nothing relevant, memors-mcp says so plainly and does not offer the closest
guess. That is deliberate. "We don't have this" is more useful than a confident wrong answer.
Claude then knows to look elsewhere. Once it finds the answer, ask it to save it, so the next
person does not have to search again.

## Too many results

If a question matches a lot, the answer says that some results were left out and suggests how
to narrow it, for example by version or by shelf. You can say:

> Only look at version 1.64.

> Only search the billing shelf.

## Asking about the past

Because old versions are kept, you can ask what the memory said at an earlier date:

> What did memors say our database version was on 1 March?
