# Security Policy

graphi is built around a **local-first, zero-egress** guarantee: parsing and
analysis run entirely on your machine, and the default binary makes no network
calls. Security reports that concern this guarantee are taken especially
seriously.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately via GitHub's
[private vulnerability reporting](https://github.com/samibel/graphi/security/advisories/new)
("Report a vulnerability" under the repository's *Security* tab).

Please include:

- a description of the issue and its impact,
- steps to reproduce (a minimal repository or input is ideal),
- the graphi version / commit and your platform,
- any suggested remediation if you have one.

We aim to acknowledge reports within a few business days and will keep you
updated on remediation progress. Coordinated disclosure is appreciated: please
give us reasonable time to ship a fix before any public disclosure.

## Supported versions

graphi is pre-1.0 and ships from `main`. Security fixes are applied to the
latest release; please upgrade (`graphi upgrade`) before reporting.

## Scope notes

- **Default binary (`graphi`)** — CGo-free, zero runtime egress. The egress
  canary and CGo-conformance CI gates enforce this; a regression here is a
  security bug.
- **Optional `graphi-broad` flavor (`-tags graphi_broad`, `CGO_ENABLED=1`)** —
  links the upstream go-sitter-forest grammar set and the Tree-sitter C runtime.
  This carries a documented residual risk when parsing untrusted source; see the
  warning in the [README](./readme.md). Treat it accordingly in your reports.
- **GitHub Action** — the GitHub token is consumed via the environment only and
  must never appear on the command line; the `extensions/github-action/validate`
  check enforces this.
