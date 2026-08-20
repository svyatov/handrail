<!--
CI already reports the build, the race and shuffle test run, the 95% coverage
gate, golangci-lint, the formatter, the doc-path check, the GoReleaser config,
and the version pin. Nothing below asks about any of that.

Delete any heading you have nothing to put under. An empty section reads as an
unanswered one.
-->

## What changes

<!-- One bullet per separable part of the change. -->

## Spec

<!--
docs/spec.md is the behavioural source of truth. If behaviour changed, name the
section you updated in this pull request. If it did not change, say why the
change is invisible to the spec.
-->

## Vocabulary and decisions

<!--
CONTEXT.md if this introduces or renames a term. A new docs/adr/ entry if this
settles a question a future reader would otherwise reopen. Say "neither" if
neither applies.
-->

## How it is proven

<!--
Which testdata/script/*.txtar case fails without this change. A new _test.go
under internal/ instead needs a sentence saying what a process boundary cannot
produce here. See docs/adr/0009-testing-strategy.md.
-->

## Harness surface

<!--
Only if this touches sync, an adapter, or the advisor. One row per harness.
Delete the whole section otherwise.

| Harness | What changes | Substitution behaviour |
|---|---|---|
| Claude Code | | |
| Codex CLI | | |
-->

## Breaking

<!--
handrail is pre-1.0, so breaking is allowed and has to be stated. Does this
change the rule file format, the nine-command surface, an exit code, or the
JSON contract of check or advise? Say "no" if it does not.
-->
