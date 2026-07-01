# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.3] - 2026-07-01

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest on a
  genuine parse error in a recognized file type (e.g. `parse: json syntax
  error in "...": invalid character '{' looking for beginning of object key
  string`) — reported via a WireMock stub `.json` file using Handlebars
  templating (`{{...}}`), which is not valid JSON. Unlike a file with no
  registered parser at all (silently skipped), a genuine syntax error is
  recorded as a `SkipParseError` diagnostic — worth surfacing, since it may
  point to a real problem — but still never aborts indexing of the rest of
  the repository.

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
