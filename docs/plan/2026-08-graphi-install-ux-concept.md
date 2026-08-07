# Installation & onboarding UX — concept

| | |
|---|---|
| **Status** | Proposed concept — owner decision pending. Records no decisions; changes no shipped contracts. |
| **Date** | 2026-08-07 |
| **Owner** | samibel |
| **Subject** | The path from "never heard of graphi" to "my agent answers through graphi": installer → first run → client wiring → doctor/upgrade → packaging |
| **Measured against** | The tree at `f47a146` (current `main`) |
| **Method** | Go source and shipped documents only. Every claim names a file and, where a value is quoted, its line. Proposed copy is marked as proposed. |

---

## 0. Why this document exists

The triggering feedback was: *"the TUI is complicated."* Taken literally, that
sentence has no target — the shipped binary contains **no TUI** at all
(`cmd/graphi/tui_disabled.go:16` prints *"this build was compiled without the
TUI surface; rebuild with: go build -tags tui ./cmd/graphi"*). The Labs TUI
that a `-tags tui` build enables is a read-only graph *explorer*, and using it
requires a two-terminal dance: start `graphi http` first, then point
`graphi tui -addr` at it (`cmd/graphi/tui_enabled.go:22-25`). Meanwhile the
docs describe a TUI that no longer exists (§3, F8).

So the complaint is read as what it evidently is: **the terminal onboarding
experience as a whole feels fragmented and complicated** — a prompt that asks
once and never again, a doctor that fails on a healthy install, a PATH hint
that trails off, a TUI that the docs promise and the binary refuses. The fix
is not a richer TUI or an install wizard. It is to make the default path need
*less* terminal interaction, not more.

**Goal, in one sentence:** from nothing to an agent answering via graphi in
under 90 seconds, with at most one question asked, and every skipped step
re-openable.

---

## 1. Principles

Derived from the product's existing contracts; P4 is the one new principle
this concept adds.

| # | Principle | Anchor |
|---|---|---|
| P1 | **Zero outbound network except user-initiated.** The installer/upgrade child process is the single sanctioned egress; graphi itself never dials. | `cmd/graphi/upgrade.go:87-98` |
| P2 | **Consent before any write to user-owned files.** No TTY → print a hint, write nothing. | `cmd/graphi/clientoffer.go:87-102`; installer never edits shell profiles (`install.sh:158-165`) |
| P3 | **One binary, zero required config.** Bare `graphi` in a repo is the whole product. | `cmd/graphi/zeroconfig.go:126-131` |
| P4 | **No one-shot is forever.** *(new)* Every "no", every missed prompt, every skipped step must print the command that reopens the door, and re-offering must be possible without editing state files by hand. | fixes `clientoffer.go:116-118` |
| P5 | **Progressive disclosure.** Bare `graphi` first; `setup` / `doctor` / `upgrade` on demand; Labs surfaces invisible until asked for. | `docs/stability-tiers.md` |

---

## 2. Current state — the golden path, with evidence

What a new user actually experiences today:

1. `curl -fsSL …/install.sh | sh` — download, checksum-verify (fail-closed),
   install to `~/.local/bin`, print a PATH hint if needed (`install.sh`).
2. `cd your-repo && graphi` — detect repo, ingest with spinner/ETA
   (`cmd/graphi/progress.go`), serve the UI on loopback, open the browser,
   print a savings line (`cmd/graphi/zeroconfig.go:131-178`).
3. One question, once, ever: `"<clients> found — connect graphi to them? [Y/n]"`
   (`cmd/graphi/clientoffer.go:108`).
4. On demand: `graphi setup` (flag-driven, no prompts), `graphi doctor`,
   `graphi upgrade`.

That skeleton is right. The friction is in the joints:

| # | Friction | Evidence |
|---|---|---|
| F1 | **"n" is forever, and silent.** The client-connect memo is written for *both* yes and no; the "n" branch prints nothing — no pointer to `graphi setup`, no re-offer ever, even when a new MCP client appears later. | `cmd/graphi/clientoffer.go:111-118` |
| F2 | **`doctor` FAILs on a healthy install.** The PATH check fails hard when `go` is absent — but the headline install path is a prebuilt CGo-free binary that needs no Go. The fallback probe list is macOS/Linux-only. | `internal/doctor/checks.go:136-148` (FAIL at `:146`), fallbacks `:28-32` |
| F3 | **PATH is a hint, not an offer.** `install.sh` prints an `export PATH=…` line and stops; `install.ps1` suggests `setx PATH "$BinDir;$env:PATH"`, which truncates `PATH` beyond 1024 characters and expands the *process* PATH into the persisted user value. | `install.sh:158-165`, `install.ps1:72-76` |
| F4 | **The version-lag machinery is structurally inert.** `KnownLatestVersion` is a `const "0.0.0"`; a Go const cannot be stamped via `-ldflags -X` (only vars can, like `internal/version.Version` — `internal/release/build.go:24,156`), and nothing in the repo regenerates the file. On any stamped release, `graphi upgrade` would report *"installed version X lags known release 0.0.0"* and `doctor`'s binary check compares against `0.0.0`. | `internal/releaseinfo/releaseinfo.go:17`, `cmd/graphi/upgrade.go:130-132`, `internal/doctor/checks.go:37-39` |
| F5 | **`upgrade --check-latest` is a documented no-op.** It prints *"latest-version check enabled (performed by child installer, not by graphi)"* and performs no lookup; no child helper exists. | `cmd/graphi/upgrade.go:121-127` |
| F6 | **Homebrew/Scoop exist but don't ship.** `cmd/gen-packaging` renders both manifests from the release matrix, drift-checked — but the committed files carry `version "0.0.0"` and all-zero hashes, release CI never regenerates them, no tap/bucket exists, and neither `readme.md` nor `site/index.html` mentions them. | `packaging/homebrew/graphi.rb:6`, `packaging/scoop/graphi.json:2`, `cmd/gen-packaging/main.go` |
| F7 | **Not-a-repo is a dead end.** Outside a `.git`/`go.work`/`go.mod` marker, bare `graphi` prints four lines and exits; there is no way to point it at a directory. | `cmd/graphi/zeroconfig.go:137-142` |
| F8 | **The TUI docs describe a deleted program.** `docs/surfaces-tui.md` says *"no `tcell`/`bubbletea`/`tview`"* and documents a `select`/`neighbors`/`blast` REPL; the code is a Bubble Tea three-pane app (`surfaces/tui/`, `go.mod:9-11`). `docs/cli-reference.md:37` and `cmd/graphi/help.go:229-233` document `tui [-db path] [-daemon socket]`; the real flag is `-addr` (`cmd/graphi/tui_enabled.go:32`). |
| F9 | **HOWTO's prerequisites contradict the quick start.** `docs/HOWTO.md:16` lists *Go 1.26.5+* as the requirement for "the `graphi` binary" — true only for building from source; the readme's headline path installs a prebuilt binary needing nothing. | `docs/HOWTO.md:12-26` vs `readme.md:28-41` |
| F10 | **MCP double-registration is a live footgun.** Two graphi entries in one client contend on the repo ingest lock; today the only surface is a doctor warning, with no repair. | `internal/mcpconfig/client.go:75-133`, `internal/doctor/checks.go:168-189` |

---

## 3. The concept: invisible installation

**The install *is* the first run.** No wizard, no installer TUI, no new
interactive surface — those would add exactly the complexity being complained
about and contradict P3. Instead, the existing golden path becomes one calm,
guided motion:

> `curl | sh` → `cd repo` → `graphi` → *[one question]* → done.

Everything else — clients, PATH, health, upgrades — is either handled with
consent inside that motion or reachable later through a named command that the
flow itself advertises. The TUI is **removed from the install story
entirely** and repositioned as what it is: a Labs explorer for people who ask
for it (§4.7).

### 3.1 Install (script)

Keep `curl -fsSL …/install.sh | sh` as the headline. Two changes, both in the
templates under `cmd/gen-install/templates/` (the committed scripts are
generated and drift-gated — `.github/workflows/release.yml`, job
`install-script-drift`):

- **PATH consent prompt** *(proposed copy)* — only when `$BINDIR` is not on
  PATH **and** stdin is a TTY:

  ```
  install.sh: installed graphi to /home/you/.local/bin/graphi
  Add ~/.local/bin to your PATH? Appends one line to ~/.zshrc [Y/n]
  ```

  "n" (or no TTY) prints the exact per-shell line to add, as today. Env
  override for automation: `GRAPHI_MODIFY_PATH=0|1` skips the question in
  either direction. This stays inside P2: an explicit yes precedes the write,
  and the write is one appended, clearly-commented line.

- **Windows: retire the `setx` suggestion** (`install.ps1:75`). Replace with
  the registry-safe form, printed or (with consent) executed:

  ```powershell
  [Environment]::SetEnvironmentVariable('Path', "$BinDir;" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')
  ```

  plus a "restart your terminal" note. `setx` truncates at 1024 chars and
  bakes the expanded process PATH into the user value — a footgun, not a hint.

### 3.2 First run (bare `graphi`)

Today's output, abridged (`zeroconfig.go:149-171`, `clientoffer.go:108`):

```
graphi: repo my-repo (branch main)
graphi: serving http://127.0.0.1:52101/
Saved $0.00 this session — savings accrue as you query graphi (e.g. via Claude Code).
(open your browser at http://127.0.0.1:52101/ if it didn't pop up)
Claude Code and Cursor found — connect graphi to them? [Y/n] n
```

Answering `n` prints nothing further; the offer never returns (F1).

Proposed *(copy to be finalized in W1; structure is the decision)*:

```
graphi 0.9.0 — local code intelligence · offline · no accounts · no telemetry

  [1/3] Indexed my-repo (branch main) — 2,341 files in 12s
  [2/3] Serving http://127.0.0.1:52101  (browser opened — GRAPHI_NO_BROWSER=1 to skip)
  [3/3] Claude Code and Cursor detected — connect graphi to them? [Y/n] n
        Skipped. Run `graphi setup` anytime — you'll only be asked again if a new client appears.

  Ready. Ask your agent: "Use graphi to show the callers of <symbol>"
  Health check: graphi doctor
```

Decisions this encodes:

- **Numbered steps** — the flow reads as one motion with a visible end. The
  step renderer reuses the existing spinner/ETA machinery
  (`cmd/graphi/progress.go`); non-TTY keeps today's bucketed log lines.
- **Every skip names its recovery** (P4): the "n" branch prints the
  `graphi setup` line; the browser line names its own suppression switch.
- **Color is optional** — TTY-gated, additive only; meaning never depends on it.
- The savings line moves out of the first-run headline (it reads as a $0.00
  shrug on a fresh install — `zeroconfig.go:154-156`) and into `graphi savings`
  / the web UI, where accrued numbers exist.

### 3.3 Re-runnable connect (the "n" fix)

Replace the boolean memo file (`clients-offered`, a single `-` byte —
`clientoffer.go:116-118`) with a **record of client IDs already offered**
(one name per line in the same state-dir file; absent file ≡ empty set).
Detection already yields precise IDs (`internal/mcpconfig/client.go`).

- Offer only clients not yet in the set; after the prompt (yes *or* no), add
  the offered IDs. The no-nag guarantee holds per client — install Windsurf
  next month and the next bare `graphi` asks once about Windsurf, nothing else.
- Migration is free: the legacy `-` content simply isn't a known client ID; on
  the next run every *currently* detected client is genuinely new to the set.
- `graphi setup` **with no flags on a TTY** becomes gently interactive: list
  detected-but-unconnected clients, one [Y/n], done. With any flag, or without
  a TTY, it stays exactly today's flag-driven, promptless behavior
  (`cmd/graphi/setup.go`) — automation observes no change.

### 3.4 Not-a-repo recovery

Keep the four-line explanation (`zeroconfig.go:137-142`), add the affordance:
**`graphi <path>`** — index and serve the named directory, same zero-config
semantics with `<path>` as cwd. One argument, no flag, consistent with P3.
(Alternative — listing candidate subdirectories — rejected: it guesses, and
guessing is how wizards start.)

### 3.5 Doctor becomes a repair tool

- **Fix the severity bug (F2):** `go` missing everywhere → **INFO**, labeled
  *"only needed for building from source"*. The prebuilt path must produce a
  clean `doctor` run (§6, AC1).
- **`graphi doctor --fix`** — each finding that has a mechanical remedy gains
  one, consent-gated per fix (`--yes` for automation), all through the
  existing atomic-write + `.bak` machinery (`internal/mcpconfig/config.go`):
  dedupe double MCP registrations (F10), repoint stale binary paths in client
  configs, offer the PATH profile append, trigger a missing-index sync.

### 3.6 Version truth

- Make `KnownLatestVersion` stampable: `const` → `var`, stamped by the release
  build exactly like `internal/version.Version` (`internal/release/build.go:156`
  already assembles the `-ldflags -X` list; add the second variable there).
  Until stamped it stays `"0.0.0"` and — new guard — a zero/empty known-latest
  **disables** lag messaging in both consumers (`cmd/graphi/upgrade.go:130`,
  `internal/doctor/checks.go:67`) instead of comparing against it.
- **`--check-latest`: implement it** (recommended over deleting): a child
  `curl`/`iwr` against the GitHub releases API, strictly opt-in, exactly the
  egress pattern `graphi upgrade` already sanctions (P1, `upgrade.go:87-98`).
  It closes the loop that build-time stamping can't: a binary can't know about
  releases newer than itself. If the owner prefers zero new egress paths,
  the honest alternative is removing the flag and its help text — not keeping
  the stub (F5).

### 3.7 Packaging and the TUI's place

- **Ship Homebrew + Scoop (F6):** wire `go run ./cmd/gen-packaging -version
  vX.Y.Z -sums SHA256SUMS` into release CI, publish to a tap/bucket, and put
  `brew install …` / `scoop install …` beside the curl line in `readme.md`
  and `site/index.html` for people who won't pipe curl into sh. Document
  `go install github.com/samibel/graphi/cmd/graphi@latest` as the from-source
  one-liner (unstamped version; say so).
- **TUI (F8):** out of the install story. Rewrite `tui_disabled.go`'s message
  to route to the default explorer — *"the graph explorer is the web UI: just
  run `graphi`. The terminal TUI is a Labs build: go build -tags tui"* — and
  do the doc truth pass (W8). Optional later: `graphi tui` self-starts an
  in-process engine so a `-tags tui` build is one command instead of two
  terminals; it already speaks the `client.Client` seam.

---

## 4. Workstreams

| # | Workstream | Touches | Effort |
|---|---|---|---|
| W1 | First-run output & copy (§3.2): numbered steps, recovery lines, savings relocation | `cmd/graphi/zeroconfig.go`, `clientoffer.go`, `progress.go` | S |
| W2 | Re-runnable connect (§3.3): memo-as-set + interactive no-flag `setup` | `cmd/graphi/clientoffer.go`, `setup.go` | S–M |
| W3 | Doctor as repair tool (§3.5): severity fix + `--fix` | `internal/doctor/checks.go`, `cmd/graphi/doctor.go`, `internal/mcpconfig/` | M |
| W4 | PATH story (§3.1): consent append, PowerShell registry form | `cmd/gen-install/templates/` (+ regenerate) | S |
| W5 | Version truth (§3.6): stampable known-latest, zero-guard, `--check-latest` decision | `internal/releaseinfo/`, `internal/release/build.go`, `cmd/graphi/upgrade.go`, `internal/doctor/checks.go` | M |
| W6 | Packaging (§3.7): gen-packaging in release CI, tap/bucket, advertise | `.github/workflows/release.yml`, `packaging/`, `readme.md`, `site/index.html` | M |
| W7 | TUI repositioning (§3.7): stub message, later optional self-start | `cmd/graphi/tui_disabled.go`, later `tui_enabled.go` | S |
| W8 | Doc truth pass: rewrite `docs/surfaces-tui.md` for the Bubble Tea reality; fix `tui` flags in `docs/cli-reference.md:37` + `cmd/graphi/help.go:229-233`; split HOWTO prerequisites into "use prebuilt" (no Go) vs "build from source" (`docs/HOWTO.md:12-26`); add brew/scoop once shipped | docs + `help.go` | S |
| W9 | Not-a-repo recovery (§3.4): `graphi <path>` | `cmd/graphi/main.go`, `zeroconfig.go` | S–M |
| W10 | *(optional)* Browser onboarding panel: a "Setup" card in the already-open web UI showing doctor status + connect buttons. An enhancement only — headless/SSH/agent environments never see the browser (`zeroconfig.go:47-57`), so the terminal path must stand alone. | `surfaces/http/`, web UI | M–L |

---

## 5. Alternatives considered

| Alternative | Verdict |
|---|---|
| **Interactive TUI install wizard** | Rejected. Adds the very complexity complained about; contradicts P3/P5. The product's strongest install UX claim is that there is almost nothing to install. |
| **Browser-first onboarding as the primary flow** | Rejected as *primary* (kept as optional W10). Agents and remote/SSH users — the core audience — never see the browser (`zeroconfig.go:47-57`). |
| **Auto-connect MCP clients without asking** | Rejected. Writes to user-owned config files without consent; violates P2 and the explicit consent gate in `clientoffer.go:87-94`. |
| **Keeping the boolean forever-memo, adding only better copy** | Rejected. Copy can't fix F1's second half: a new client appearing later deserves a fresh offer. The set-based memo keeps the no-nag guarantee and is a smaller diff than it sounds. |

---

## 6. Phased roadmap & acceptance criteria

**Phase 0 — quick wins (copy + docs, no behavior change):**
W1 strings, the "n" recovery line, W8 doc truth pass, the F2 severity fix,
W7 stub message.

**Phase 1 — the joints:** W2 memo-as-set + interactive setup, W3 `--fix`,
W4 PATH consent, W5 version truth.

**Phase 2 — reach:** W6 packaging shipped and advertised, W9 `graphi <path>`,
optional W10 browser panel, optional TUI self-start.

Acceptance criteria (each mechanically checkable):

- **AC1** Fresh prebuilt install on a machine without Go: `graphi doctor` exits 0.
- **AC2** Answer "n" to the connect offer, then install a new MCP client:
  the next bare `graphi` re-offers for that client only.
- **AC3** The "n" branch and the non-TTY branch each print a line naming `graphi setup`.
- **AC4** A stamped release binary never reports a version lag against `0.0.0`.
- **AC5** `brew install` / `scoop install` land the same checksummed asset the
  script installs.
- **AC6** `curl` → first agent answer in under 90 seconds on a mid-size repo;
  the flow asks at most one question, and zero without a TTY.
- **AC7** No new required prompt anywhere; every new prompt has a non-TTY
  fallback and an env override.

---

## 7. Out of scope

Accounts, telemetry, or any phone-home (permanently); a daemonized
auto-updater; a GUI installer; changes to the GA operation set; a TUI
redesign (the TUI is repositioned, not rebuilt).

## 8. Open questions for the owner

1. **Tap/bucket location** — `samibel/homebrew-graphi` + `samibel/scoop-graphi`,
   or serve both from this repo? (Separate repos are the ecosystem default and
   keep release artifacts out of the source tree.)
2. **`--check-latest`** — implement the opt-in child lookup (recommended,
   §3.6) or delete the flag? Keeping the stub is the one option this concept
   rules out.
3. **`graphi <path>`** — confirm the argument form (vs. a `-root` flag on the
   zero-config path) before W9.
