# Part III: Configuration reference

memors-mcp is configured through environment variables, command flags and one optional file.
There is no configuration file for the server itself.

- [Environment variables](environment.md): every variable, its default and its effect.
- [Commands and flags](commands.md): every command and flag.
- [profiles.json](profiles-json.md): ranking-profile overrides.
- [Files on disk](files.md): what memors-mcp creates and where.

**Precedence.** A flag beats its environment variable, which beats the built-in default.
Examples: `serve --metrics-addr` over `MEMORS_METRICS_ADDR`, `search --profile` over
`MEMORS_PROFILE`.
