# Privacy and safety

## Where your information lives

Everything is stored in **one file on your computer**, in a folder called `.memors-mcp` in your
home folder. Only your user account can open that folder. There is no cloud copy, no account
and no sync.

## What leaves your computer

**Almost nothing.** The first time memors-mcp starts, it downloads a language model of about
140 MB from Hugging Face, a public model library. That is the only time it contacts the
internet. After that it works fully offline.

memors-mcp itself never visits websites. When you ask Claude to save a web page, Claude fetches
it and hands over the text.

There is no telemetry, no usage reporting and no analytics.

## Two things to be aware of

1. **The memory file is not encrypted.** Anyone who can open files in your home folder can read
   it. Do not store passwords or secrets in it. If you saved one by mistake, remove it
   completely; see [Forgetting things](everyday/forgetting.md).
2. **Claude sees what it saves and searches.** The words go through Claude, as everything in a
   conversation does. memors-mcp adds no extra exposure, but it does not hide anything from Claude
   either.

## Safety rules built in

- **Claude cannot vouch for itself.** Only a person can raise an item's trust. See
  [Trust](trust.md).
- **Claude cannot delete your records.** It can only remove what Claude saved.
- **Nothing is silently overwritten.** Changes create new versions, and the old ones stay in
  history.
- **The browser view cannot change anything,** and it is only reachable from your own
  computer.
- **The optional activity log is off by default.** If you turn it on, it records which tools
  were used and how long they took, but never the text you saved or read.

## Backing up and moving

Your memory is a single file, so backing it up is copying it. See
[Questions and troubleshooting](faq.md) for moving it to another computer.
