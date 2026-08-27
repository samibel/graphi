# Rolling back the executor seam

**Audience:** whoever operates a graphi install — the person who can restart the
MCP server or the daemon.
**Applies to:** the AX-06/AX-08 executor seam (`surfaces/client/canary.go`) and
its `GRAPHI_CANARY_*` kill switch.
**Status:** Labs internals. Nothing on this page changes a Stable operation, a
wire name, or a result byte.

graphi serves a small set of Labs operations through an internal *executor*
path that sits beside the original *legacy* path. Which one answers is decided
per operation by an environment variable, and the shipped position is `legacy`
— the pre-executor code, unchanged. This page is how you move that switch, how
you confirm it moved, and how you put it back.

## 1. The operations on the seam

Ten operations dispatch through the seam. Every one of them is Labs; none is
part of the frozen Stable 12.

| Operation | Kill-switch variable |
|---|---|
| `architecture` | `GRAPHI_CANARY_ARCHITECTURE` |
| `architecture_violations` | `GRAPHI_CANARY_ARCHITECTURE_VIOLATIONS` |
| `compound` | `GRAPHI_CANARY_COMPOUND` |
| `dead_code` | `GRAPHI_CANARY_DEAD_CODE` |
| `find_clones` | `GRAPHI_CANARY_FIND_CLONES` |
| `framework_map` | `GRAPHI_CANARY_FRAMEWORK_MAP` |
| `repo_overview` | `GRAPHI_CANARY_REPO_OVERVIEW` |
| `search_ast` | `GRAPHI_CANARY_SEARCH_AST` |
| `search_hybrid` | `GRAPHI_CANARY_SEARCH_HYBRID` |
| `test_impact` | `GRAPHI_CANARY_TEST_IMPACT` |

The variable name is always `GRAPHI_CANARY_` + the operation id in upper case.
`GRAPHI_CANARY_ALL` sets the position for every operation at once.

## 2. The three positions

| Position | What runs | What the caller gets |
|---|---|---|
| `legacy` **(shipped default)** | the legacy method only | the legacy result |
| `shadow` | both paths, compared | the **legacy** result |
| `active` | the executor path only | the executor result |

`shadow` runs every call twice. It is an investigation position, not a
production one: it roughly doubles the work for the operations it covers and it
exists so a divergence can be *recorded* (see §5). `active` makes the executor
authoritative and is the position a rollback undoes.

An unrecognised value **fails the session at startup** rather than falling back
to a default — a typo like `GRAPHI_CANARY_DEAD_CODE=lecacy` must not leave you
believing you rolled back when you did not.

## 3. Forcing `legacy`

### Everything at once

```sh
GRAPHI_CANARY_ALL=legacy graphi mcp
```

### One operation

```sh
GRAPHI_CANARY_DEAD_CODE=legacy graphi mcp
```

A per-operation variable **wins over** `GRAPHI_CANARY_ALL`, so you can pin the
whole seam to one position and carve out a single operation:

```sh
GRAPHI_CANARY_ALL=shadow GRAPHI_CANARY_DEAD_CODE=legacy graphi mcp
```

### Scope — read this before you conclude it did not work

* **Per process, not global.** The position is installed from the environment
  when a session is composed. It governs the process that reads it and nothing
  else. There is no config file, no daemon-wide setting, and no remote switch.
* **Read at startup.** A running `graphi mcp`, `graphi serve` or `graphi daemon`
  keeps the position it started with. **Restart it** for a change to take
  effect.
* **It must be in the SERVER's environment.** For an MCP client that launches
  `graphi mcp` itself, exporting the variable in your terminal changes nothing —
  put it in the `env` block of that client's MCP server entry (`.mcp.json`,
  `claude_desktop_config.json`, …) and restart the client.
* **The default needs no variable at all.** Unset is `legacy`.

## 4. Verifying the switch took effect

`graphi doctor` reports the live position of every migrated operation, with the
source of that position — the compiled-in default or the variable that
overrode it. Run it **in the same environment as the server**:

```sh
$ graphi doctor
…
executor-seam  10 migrated operation(s): 10 legacy, 0 shadow, 0 active
```

```sh
$ graphi doctor --json | jq '.checks[] | select(.id=="executor-seam")'
```

The check's detail lists one line per operation, e.g.
`dead_code: legacy (GRAPHI_CANARY_DEAD_CODE)` when a variable set it, or
`dead_code: legacy (compiled-in default)` when nothing did.

`doctor` reports **this process's** positions, derived from **this**
environment. That is the honest scope: a server started from the same
environment is in the same position. If your MCP client launches the server with
its own `env` block, check that block — do not infer it from a shell.

## 5. Reading the divergence record

While any operation is in `shadow`, graphi compares the two paths on every call
and persists what it saw to the graphi state directory
(`$XDG_STATE_HOME/graphi/executor-divergence/`, else `~/.graphi/…`). The record
survives a restart and is read **without starting a server**:

```sh
$ graphi doctor -divergence
$ graphi doctor -divergence --json
```

Each operation reads as one of:

| State | Meaning |
|---|---|
| `UNKNOWN` | **no** dual-run observation was ever recorded for it |
| `NO-DIVERGENCE-OBSERVED` | it was observed, and every observation matched |
| `DIVERGED` | at least one observation found the two paths different |

`UNKNOWN` is the expected state on a normal install, because the shipped
position compares nothing. **It is not a statement that the two paths agree.**
Do not read an all-`UNKNOWN` record as evidence of parity; it is the absence of
evidence, which is why it has its own word.

### What the totals do and do not promise

The counts are a **lower bound whenever the record says so**, and it says so in
place rather than leaving you to infer it. The header line reports segments
three ways — `N recorded, M unreadable, P pruned` — and a non-zero `M` or `P`
prints an explicit paragraph saying the totals below are incomplete. `graphi
doctor` repeats both in the `executor-divergence` check's detail.

Two things can make it a lower bound:

* **Unreadable segments.** A file in the directory that does not parse is
  counted and disclosed, never silently skipped.
* **Pruned segments.** One process writes one segment file, and the directory
  retains at most **64** of them; a flush that would exceed that deletes the
  oldest. Be precise about what that can take: pruning sorts by modification
  time and has **no writer-liveness check** beyond the pruning process refusing
  to delete its own segment. A server that is still running but has been quiet
  since its last flush looks exactly as old as one that exited months ago, so
  its already-written counts can be deleted while it is live — and it will not
  rewrite them, because its in-memory total is only re-serialised when it next
  observes something. Reaching that at all takes 64+ distinct writer segments,
  which a single-or-few-server install does not produce. Every prune is counted
  into the pruning process's own segment (and carried forward if the segment it
  deleted was itself carrying a count), which is what turns this from silent
  loss into a disclosed lower bound.

A **mismatch is never at risk** from either: divergences are written the moment
they are observed, before anything can coalesce or be pruned. What a lower bound
costs you is precision in the observation *count*, not the finding.

Within one running process the counts are not lost by reconfiguration: an MCP
client announcing a roots-list change makes the server re-bind mid-session, and
that path reuses the live record rather than replacing it, flushing before it
ever lets one go.

Rolling an operation back to `legacy` stops the comparison, so the record stops
growing. It is not deleted — the history of what was observed while the seam was
open stays readable. To discard it, remove the
`executor-divergence/` directory from the state directory; graphi recreates it
only when it observes something again.

## 6. Returning to the prior setting

1. **Unset** the variable (or remove it from the MCP client's `env` block):
   ```sh
   unset GRAPHI_CANARY_ALL GRAPHI_CANARY_DEAD_CODE
   ```
2. **Restart** the server or daemon.
3. **Confirm** with `graphi doctor` that every line reads
   `… (compiled-in default)` again:
   ```sh
   $ graphi doctor --json | jq -r '.checks[] | select(.id=="executor-seam") | .detail'
   ```

Setting the variable explicitly back to `legacy` gets you the same *behaviour*
as unsetting it, and `doctor` will tell the two apart: it names the variable as
the source instead of the compiled-in default. Prefer unsetting, so the next
person reads the shipped configuration rather than a pinned one.

## 7. What a rollback does not touch

* No Stable operation is on this seam, so a rollback cannot change a Stable
  wire name, request schema, canonical result byte, error code, or the default
  MCP tool profile.
* The record in §5 is local files only. Writing it makes no network call, and
  reading it starts no server.
* Rolling back does not re-index, migrate, or invalidate anything on disk.

## 8. Exercised, not just described

`.github/workflows/executor-rollback.yml` runs this page's procedure on every
pull request: it forces `GRAPHI_CANARY_ALL=legacy`, runs the parity and
characterization suites in that position, asserts the divergence read path is
honest, and then asserts the round trip — that unsetting the variable returns
every operation to the compiled-in default. A rollback that stopped working
would fail CI rather than fail an operator.
