# How a condition reads many values

ADR 0011 made a canonical field hold one or more values, and its first reading of `not_X` was "no value matches X". That makes every allowlist bypassable by one extra command: `not_starts_with: git` does not fire on `git status; rm -rf /`. The hole is older than the parser, since a single string starting with `git` already let it through, and the same hole reaches `network_grant`, `url`, a rename's paths, and every value later decisions added. Reversing the quantifier alone breaks `tool`, whose values are names for one call, so the model now says what a value is. ADR 0011 and ADR 0014 point here for how their values are read.

A **Candidate** is one thing the call does; a **Spelling** is one way of writing it. A rule matches when some choice of one Candidate per field makes its conditions true, and a Candidate meets a term when any of its Spellings does. Positive terms read as before. A `not_` term matches when some Candidate has no Spelling that matches, the allowlist reading Claude Code's own allow rules use. Terms on one field bind to one Candidate, so an exception excepts the call it names rather than any call in the same program. Terms on different fields choose independently.

Under a hostile agent every choice leans closed: each wrapping level and each leading assignment prefix is its own Candidate, so allowing a wrapper allows no command behind it; each redirect to a file is its own Candidate; a hard parse failure makes a `not_` term on `command` match, since bash runs the lines before a syntax error; and the whole line, which always contains an allowed command's text, is never read by a `not_` term.

A single `not_` term fires on every call it fired on before, and more. Binding narrows rules that matched only by combining two Candidates. Both change what existing rules match, so this is a MAJOR change, and ADR 0003's Examples report it at the first `SessionStart` after the upgrade. Allowlists must name the wrappers, assignment prefixes and redirects they accept, which is noise and never a bypass. A pattern only the whole line meets cannot carry a `not_` exception on the same field.

## Considered options

Keeping "no value matches" leaves allowlists unwritable. Flat "some value does not match" fires `tool not_equals: Task` on every `Agent` call, because an MCP call and a renamed tool carry several names for one call. An opt-in quantifier keyword leaves the default bypassable, and the default is what a skill or a hurried user writes.
