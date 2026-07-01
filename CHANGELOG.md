# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest on the
  first `.json` file that is not valid strict JSON (`parse: json syntax error
  in "...": invalid character '{' looking for beginning of object key
  string`) — reported on a WireMock `__files` response body that uses
  Handlebars response-templating (`{{...}}` at a structural position), which
  WireMock renders at runtime but is not valid strict JSON. More generally,
  any genuine parse/syntax error in a file that DOES have a registered parser
  is now a recorded `SkipParseError` diagnostic (fail-closed skip) instead of
  a hard error, matching the existing oversize/timeout/max-depth/unreadable
  pattern. A single malformed file can no longer sink a FULL index of the rest
  of the repository. The INCREMENTAL path (`IngestChanged`, used by the edit
  applier and the filesystem watcher) stays strict: if a file it was asked to
  reparse no longer parses, it returns a hard error so its metadata transaction
  rolls back atomically — keeping the metadata sidecar consistent with the
  graphstore, and letting the edit saga compensate (roll back) an edit that
  produces source the parser rejects. Only PRE-EXISTING malformed files (seen
  by the full index) are tolerated.

## [0.1.2] - 2026-07-01

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest on the
  first file with no registered parser (`parse: no parser registered for
  file type`) — reported via a macOS `.DS_Store` file, but the same crash
  applied to any image, font, PDF, lockfile, or other unrecognized-extension
  asset, which is the overwhelming majority of non-source files in a
  typical repository. This is now a silent, expected skip (not a recorded
  diagnostic — finding a non-source file isn't noteworthy), matching the
  existing fail-closed pattern already used for oversize/timeout/max-depth/
  unreadable files.

## [0.1.1] - 2026-07-01

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest when a
  repository contains a symlink whose target is a directory — the pnpm
  `node_modules/.pnpm` layout links whole package directories this way, and
  hit this on a real-world JS/TS repo (`EISDIR` while reading the symlink as
  if it were a regular file). Any unreadable path (symlink-to-directory,
  broken symlink, permission denied) is now a recorded `SkipUnreadable`
  diagnostic instead of a hard error, matching the existing
  oversize/timeout/max-depth fail-closed skip pattern.
- `node_modules`, `.git`, `vendor`, `.venv`/`venv`, `__pycache__`, and
  `bower_components` are now pruned from indexing entirely (never
  descended into), on both the initial full index and the live filesystem
  watcher. These hold dependency trees or VCS metadata, not a repository's
  own code — besides being where the pnpm symlink layout above lives,
  indexing them was slow and drowned query results in third-party noise.

## [0.1.0] - 2026-06-28

### Added
- First tagged release. The `release-assets` CI job cross-compiles the
  `internal/release.ReleaseTargets` matrix (linux/darwin amd64+arm64,
  windows amd64), generates `SHA256SUMS`, and uploads every asset to the
  GitHub Release — so the one-line installer's
  `releases/latest/download/...` URLs resolve instead of returning 404.
- Open-source community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, issue templates, and a pull-request template.

<!--
When cutting a release, move entries from Unreleased into a new section, e.g.:

## [0.1.0] - 2026-06-28
### Added
- ...
-->
