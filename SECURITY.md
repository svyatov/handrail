# Security policy

## Reporting a vulnerability

Report privately through GitHub, at
[Security, Report a vulnerability](https://github.com/svyatov/handrail/security/advisories/new).
The report is visible only to the maintainer until an advisory is published.

Do not open a public issue for a vulnerability.

You will get a first response within 14 days. That response says whether the
report is accepted, and if it is, what the fix and disclosure timeline looks
like. If 14 days pass with no reply, open a public issue saying only that a
private report is awaiting a response, with no details of the vulnerability
itself.

## What is in scope

handrail runs as a hook inside a coding agent harness and decides whether a
tool call is allowed. Reports of the following are in scope:

- A rule that should block a tool call but does not, or that can be made not to.
- A path through `handrail sync` that writes a harness configuration the rule
  files did not ask for.
- A committed `.handrail/` ruleset taking effect without `handrail trust`.
- Anything that lets a rule file execute code, read a file, or reach the network.
- A flaw in the plugin bootstrap's download or checksum verification.

Out of scope: a harness that never calls the hook, and a rule that is simply
written wrong. Both are ordinary issues.

## Supported versions

handrail is pre-1.0. Only the latest release gets fixes; there are no backports
to earlier tags.
