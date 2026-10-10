# Words we use

| Word | What it means here |
|---|---|
| **Address** | A short link that starts with `memo://`, pointing at exactly one saved item. It keeps working even if the item is later forgotten |
| **Agent** | An AI assistant, such as Claude, that uses tools on your behalf. Also the lowest trust level: "Claude saved this" |
| **Browser view** | The read-only web page that shows your memory: `memors-mcp ui` |
| **Curated** | The highest trust level: checked and approved as a reference source |
| **Degraded** | Searching works, but only by matching words, because the meaning-matching model is not ready |
| **Document** | Anything longer you saved: a note, a guide, a web page, meeting notes |
| **Embedding** | The way a language model turns text into numbers so it can match meaning rather than exact words. You never need to handle them |
| **Fact** | One short statement worth keeping on its own, with a date range and a source |
| **Forget** | Remove an item from searches, keeping a record that it was removed, when and why |
| **History** | Earlier versions of documents and facts, kept when something changes |
| **Language model** | The downloaded file that lets memors-mcp match by meaning. It is downloaded once and runs on your computer |
| **MCP** | Model Context Protocol: the standard way Claude plugs into tools like memors-mcp |
| **Memory, or knowledge base** | Everything memors-mcp holds for one project, kept in one file |
| **Page** | A summary Claude wrote about one topic, built from saved passages and pointing back to them |
| **Passage** | A short piece of a document, a few paragraphs long. Searches find passages, then show the document they came from |
| **Promote** | Raise an item's trust level. Only a person can do it |
| **Shelf** | A label that groups documents within one memory, such as `billing` or `infrastructure`. Technically called a namespace |
| **Stale** | A summary page whose sources changed after it was written; it needs a refresh |
| **Trust** | Who has vouched for an item: Claude (agent), a person (user), or a checked reference (curated) |
| **User** | The middle trust level: a person saved or confirmed it |
