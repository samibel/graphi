# Rolling back the executor seam

**Audience:** whoever operates a graphi install — the person who can restart the
MCP server or the daemon.
**Applies to:** the AX-06/AX-08 executor seam (`surfaces/client/canary.go`) and
its `GRAPHI_CANARY_*` kill switch.
**Status:** Labs internals. Nothing on this page changes a Stable operation, a
wire name, or a result byte.
**Shipped default changed:** SW-244 (2026-08-28) moved it from `legacy` to
`shadow`. If you are reading an older copy of this page, §2 and §4 below are the
paragraphs it gets wrong.

graphi serves a small set of Labs operations through an internal *executor*
path that sits beside the original *legacy* path. Which one answers is decided
per operation by an environment variable, and the shipped position is
**`shadow`** — both paths run, and **the caller still receives the legacy
result, byte for byte**. This page is how you move that switch, how you confirm
it moved, and how you put it back.

**Start here if something is wrong.** The rollback is
`GRAPHI_CANARY_ALL=legacy` plus a restart (§3), and it is complete the moment
the next call starts. Nothing is keyed on the position — no schema, no persisted
state, no cached artifact, no wire identifier — so there is nothing to migrate
back.

## 1. The operations on the seam

Eleven operations dispatch through the seam. Every one of them is Labs; none is
part of the frozen Stable 12.

`search_semantic` joined the seam in SW-265 (2026-09-02). Its four-state
determinism fixtures — `configured | unavailable | stale | corrupt` — pinned
every path as byte-stable across runs, and the per-operation kill switch
`GRAPHI_CANARY_SEARCH_SEMANTIC` defaults to `shadow`. The contract is identical
to the existing ten: legacy bytes are what the caller receives, the executor
path is compared and recorded but never returned, and a deterministic-regress
test (`TestExecutorParity_SearchSemanticFourStates`) must fail before the id is removed from
`migratedOperations`.

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
| `search_semantic` | `GRAPHI_CANARY_SEARCH_SEMANTIC` |
| `test_impact` | `GRAPHI_CANARY_TEST_IMPACT` |

The variable name is always `GRAPHI_CANARY_` + the operation id in upper case.
`GRAPHI_CANARY_ALL` sets the position for every operation at once.

## 2. The three positions

| Position | What runs | What the caller gets |
|---|---|---|
| `legacy` | the legacy method only | the legacy result |
| `shadow` **(shipped default)** | both paths, compared | the **legacy** result |
| `active` | the executor path only | the executor result |

`shadow` runs every call twice, and it is what a normal install runs. What that
buys is §5: every call compares the two paths and persists what it saw, so a
divergence is a thing you can read rather than a thing someone has to reproduce.

**What it costs, measured rather than estimated** (`docs/rc/ax06-canary-latency.md`
§6 and §7, one fixture, one machine). Since SW-245 the second path does **not**
run on the thread that serves your request: the caller is answered from the
legacy method and the comparison runs afterwards on a background worker. That
splits the cost in two, and both halves are stated here because only one of them
went away.

* **What the caller waits for: essentially nothing.** Under the measurement
  method AX-06 uses (a rotating A/B on a machine with headroom, N = 200 per
  arm), `shadow`'s p50 is **0.973× legacy** and its p95 **0.918×** — inside the
  run's own noise band, i.e. not separable from `legacy` at that resolution. It
  was **2.05×** at p50 before SW-245.
* **What the machine still pays: the whole second path.** CPU and allocations
  are **unchanged at about 2.0× legacy** — 3 867 allocations per call against
  legacy's 1 905. Moving work to another goroutine does not stop it costing.
  On a box with spare capacity you will not feel it in request latency; on a
  **saturated or single-CPU** host you will, and the same benchmark reads
  **1.26×** under a back-to-back single-caller loop and **1.89×** at
  `GOMAXPROCS=1`.

It does **not** double the work of a graphi session: the eleven operations are Labs,
and nothing else on any surface is on this seam.

Because the comparison is deferred, it is also **bounded**: at most 64 comparisons
may be waiting at once, and a process that outruns that loses the surplus. Those
losses are counted and printed — see §5's coverage line — and are the normal
outcome on a host with no spare CPU. They are a gap in the evidence, never
evidence of agreement.

`shadow` cannot change an answer, and since SW-245 it cannot change a *timing*
either. The bytes the caller receives are the legacy method's own return value,
byte for byte what `legacy` returns; the executor's result is compared and
recorded and never returned; and the caller does not wait for that comparison to
finish. If you need the second path to stop running anyway — during an incident,
or to reclaim the CPU and allocations — §3 is that switch.

`active` makes the executor authoritative. It is **not** a shipped position and
is the one a rollback most urgently undoes.

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
* **The default needs no variable at all.** Unset is `shadow` — which means a
  rollback to `legacy` is something you must set **explicitly**, and unsetting
  the variable later puts the seam back into `shadow` (§6).

## 4. Verifying the switch took effect

`graphi doctor` reports the live position of every migrated operation, with the
source of that position — the compiled-in default or the variable that
overrode it. Run it **in the same environment as the server**:

```sh
$ graphi doctor
…
executor-seam  10 migrated operation(s): 0 legacy, 10 shadow, 0 active;
               NONE of the 11 dual-running operation(s) is reachable through `graphi mcp`
```

That line — `10 shadow` — is what an install with **nothing set** reports. After
a rollback it reads `10 legacy, 0 shadow, 0 active`. The clause after the
semicolon is SW-248: the counts say what is **configured**, and it says what a
client can **call**. On a stock install the answer is *none of it* — see §5.

```sh
$ graphi doctor --json | jq '.checks[] | select(.id=="executor-seam")'
```

The check's detail lists one line per operation, e.g.
`dead_code: legacy (GRAPHI_CANARY_DEAD_CODE)` when a variable set it, or
`dead_code: shadow (compiled-in default)` when nothing did. Since SW-248 each
line also states whether any shipped profile can reach the operation, e.g.
`dead_code: shadow (compiled-in default), NOT in the default profile; reachable
via graphi mcp -labs` — the counts say what is *configured*, and that clause says
what a client can *call*. See §5's reachability subsection.

`doctor` reports **this process's** positions, derived from **this**
environment. That is the honest scope: a server started from the same
environment is in the same position. If your MCP client launches the server with
its own `env` block, check that block — do not infer it from a shell.

## 5. Reading the divergence record

While any operation is in `shadow`, graphi compares the two paths on every call
it can and persists what it saw to the graphi state directory
(`$XDG_STATE_HOME/graphi/executor-divergence/`, else `~/.graphi/…`). The record
survives a restart and is read **without starting a server**:

```sh
$ graphi doctor -divergence
$ graphi doctor -divergence --json
```

A readout looks like this (three MCP tool calls through `graphi mcp -labs` on a
fresh install):

```
executor-seam divergence record (executor-divergence-v1)
  state:      PARTIAL-UNKNOWN — some migrated operations have never been observed
  directory:  /home/you/.local/state/graphi/executor-divergence
  segments:   3 recorded, 0 unreadable, 0 pruned
  totals:     3 observation(s), 0 mismatch(es)
  coverage:   3 of 3 dispatch(es) compared (100%) — no sampling, nothing dropped
  reachable:  NONE of the 11 operation(s) on the seam is reachable through `graphi mcp` (the profile a stock install binds)

OPERATION      DISPATCHES  OBSERVATIONS  SKIPPED  MISMATCHES  STATE                   REACHABLE VIA     …
dead_code      1           1             0        0           NO-DIVERGENCE-OBSERVED  graphi mcp -labs  …
repo_overview  1           1             0        0           NO-DIVERGENCE-OBSERVED  graphi mcp -labs  …
compound       0           0             0        0           UNKNOWN                 graphi mcp -labs  …
```

Each operation reads as one of:

| State | Meaning |
|---|---|
| `UNKNOWN` | **no** dual-run observation was ever recorded for it |
| `NO-DIVERGENCE-OBSERVED` | it was observed, and every observation matched |
| `DIVERGED` | at least one observation found the two paths different |

`UNKNOWN` means **no** dual-run observation was recorded for that operation. It
is **not a statement that the two paths agree**. Do not read an all-`UNKNOWN`
record as evidence of parity; it is the absence of evidence, which is why it has
its own word.

### Reachability — why an operation is `UNKNOWN` (SW-248)

Since SW-244 the shipped position *does* compare, so an operation that gets
called moves off `UNKNOWN`. Whether it *can* be called is a separate question,
and it is the one this section exists for.

Every operation on the seam is **Labs**. The default MCP profile — what `graphi
setup` registers and what a stock client binds — advertises the **eleven Stable
tools** and none of them. So a client on the default profile cannot call one of
these operations at all, and their rows stay `UNKNOWN` however long the install
runs. That is not a coverage gap in your usage; it is a property of the profile.

The readout says which of the two you are looking at. Three shapes, three
different sentences:

| What you see | What it means |
|---|---|
| `NOT YET OBSERVED, but reachable: …` | the bound profile advertises these; a call records an observation |
| `NOT OBSERVABLE through \`graphi mcp\`: …` | the bound profile does not advertise these; no amount of use will record one |
| `NOT REACHABLE THROUGH ANY SHIPPED PROFILE: …` | nothing can observe these — a build defect, not a setting |

And when *nothing* on the seam is reachable through the default profile — the
state of a stock install today — the document's overall verdict says so rather
than reporting a bare `UNKNOWN`:

```
  state:      UNKNOWN-AND-UNOBSERVABLE — no dual-run observation has been recorded AND
              none can be: `graphi mcp` reaches nothing on the seam
```

```
THIS RECORD CANNOT FILL in `graphi mcp`: not one of the 11 operation(s) on the seam is
reachable there. Its emptiness is therefore evidence about the PROFILE, not about
the two paths, and waiting longer will not change it.
```

To observe these operations, bind a profile that advertises them —
`graphi mcp -labs`. The `REACHABLE VIA` column names it per operation, and
`graphi doctor`'s `executor-seam` check names it per operation too.

This is disclosure, not a promotion. No tier moved: the Stable-12 and the
eleven-tool default profile are unchanged, and `go run ./cmd/seamreach -check`
is the CI gate that refuses a future migration putting an operation on the seam
with no shipped profile that reaches it.

### Coverage — how much of what happened was actually compared

Read the `coverage:` line before you read the totals. Since SW-245 the comparison
runs on a background worker with a bounded queue, so a call can reach the seam
and never be compared. The record therefore reports **three** numbers per
operation, not one:

* **DISPATCHES** — calls that reached the seam and were candidates for comparison.
* **OBSERVATIONS** — calls that were actually compared.
* **SKIPPED** — the difference, with its cause.

When nothing was skipped the header says so outright (`3 of 3 dispatch(es)
compared (100%) — no sampling, nothing dropped`). When something was, it says
that instead, names the cause, and prints a paragraph under the table:

```
  coverage:   612 of 640 dispatch(es) compared (95.6%) — 28 NOT compared (queue-full=28)
…
28 dispatch(es) reached the seam and were NOT compared (queue-full=28), so the
observation counts above cover 95.6% of what the seam saw. A call that was not
compared is NOT evidence that the two paths agree — it is a coverage gap, …
```

The three causes you can see:

* **`queue-full`** — calls arrived faster than the worker could compare them and
  the 64-deep queue was full. Expect this on a busy or CPU-starved host; it is
  the bound doing its job rather than the process growing without limit. It is
  the *normal* reading at `GOMAXPROCS=1`.
* **`drain-abandoned`** — the process was shutting down and ran out of its
  5-second drain budget with comparisons still queued. This should be rare; a
  standing non-zero count here means comparisons are routinely outliving the
  sessions that started them.
* **`caller-cancelled`** — the caller hung up or timed out *while the legacy
  method was still running*, so the legacy outcome was `context canceled` /
  `context deadline exceeded` rather than a result. There is nothing comparable
  to compare: the deferred pass runs on a context that survives the caller (it
  has to, or the comparison would be cancelled the instant the request ends), so
  it would succeed and the pair would be recorded as an `error-presence`
  divergence manufactured by the shutdown of the request. Expect a low steady
  count on an HTTP surface with impatient clients; a *large* one means callers
  are giving up on the legacy path, which is a latency problem, not a parity
  one.

**There is no sampling.** graphi does not compare a fraction of calls on purpose;
every dispatch is queued for a full byte-exact comparison, and the only way one
is missed is one of the three causes above, all of which are counted. So a
`SKIPPED` of zero means what it says.

**Record format.** The `executor-divergence-v1` document gained three additive
fields with SW-245 — `dispatches` and `skipped` per operation and per document,
and `coverage` — alongside the existing observation and mismatch counts. The
schema tag is unchanged because the addition is backward compatible in both
directions: an older reader ignores the new keys, and a newer reader treats a
record written without them as `skipped: 0` and derives `dispatches` from the
observations, which is exactly what a pre-SW-245 record meant. `graphi doctor`
and the CI leg read the new fields; nothing else does.

SW-248 added the reachability axis: `reach_evaluated`, `default_profile`,
`reachable_in_default`, `unobservable_in_default`, `unreachable_anywhere` and an
echo of `profiles` on the document, plus `reach` and `reached_by` per operation.
Those are additive in the same sense. **One change is not, and is stated here
rather than left to be discovered:** the document-level `state` gained the value
`UNKNOWN-AND-UNOBSERVABLE`, which replaces a bare `UNKNOWN` when the record is
empty *and* nothing on the seam is reachable through the default profile. A
reader comparing `state == "UNKNOWN"` stops matching a fresh install. That is
the point — the two conditions were being reported under one word, and doing so
is the defect SW-248 closed. The persisted **segment** format is untouched, so
nothing on disk needs migrating.

`graphi doctor`'s own `executor-divergence` check refuses to report **PASS** while
anything is skipped, even when every operation was observed and none diverged:
partial evidence is INFO with the numbers, because PASS is what a reader scanning
statuses treats as "proven".

### What the totals do and do not promise

The counts are a **lower bound whenever the record says so**, and it says so in
place rather than leaving you to infer it. The header line reports segments
three ways — `N recorded, M unreadable, P pruned` — and a non-zero `M` or `P`
prints an explicit paragraph saying the totals below are incomplete. `graphi
doctor` repeats both in the `executor-divergence` check's detail.

Three things can make it a lower bound:

* **The last two seconds of a process that is KILLED.** A store writes its
  **first** observation immediately and every **mismatch** immediately;
  everything in between coalesces and is written at most once every two seconds.
  A server killed inside that window loses the coalesced counts it was still
  holding — a short-lived session that made five calls in a tenth of a second
  would persist as **one** observation, not five. This is a *count* imprecision
  and never a lost finding: mismatches do not coalesce.

  SW-245 closed this for every session that **ends normally**. Because the
  comparison is now deferred, a session that walked away would have left findings
  in a queue as well as counts in a buffer, so shutting a session down now drains
  the queue *and* flushes the buffer before releasing anything. A graceful exit —
  including a graceful rollback (§3, `GRAPHI_CANARY_ALL=legacy` + restart) —
  therefore persists everything **that session** observed. What is left is the
  case that was always unrecoverable: a `SIGKILL`, a crash, or a power loss,
  where anything still queued or buffered goes with the process.

  Be precise about which sessions those are. The drain-and-flush belongs to the
  MCP session lifecycle (`graphi mcp`, and `graphi doctor`, which apply the
  kill-switch positions and install the recorder); replacing or retiring the
  recorder mid-process — a state-directory repoint, or a rollback to all-`legacy`
  applied without a restart — drains first for the same reason. `graphi http`
  drains its queue at exit too, so no comparison runs against a store that is
  closing under it, but that surface never installs a recorder and never applies
  the kill switch, so it has no durable record to flush and no per-operation
  override to honour in the first place. Its comparisons are in-process only.
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

**Unsetting returns the seam to `shadow`, not to `legacy`.** That is the point
of this section — the prior setting is the shipped one — but it is the step
most likely to surprise someone who rolled back yesterday. If you meant to stay
on `legacy`, keep the variable set; `doctor` will name it as the source instead
of the compiled-in default, so the two are always distinguishable.

## 7. What a rollback does not touch

* No Stable operation is on this seam, so a rollback cannot change a Stable
  wire name, request schema, canonical result byte, error code, or the default
  MCP tool profile.
* The record in §5 is local files only. Writing it makes no network call, and
  reading it starts no server.
* Rolling back does not re-index, migrate, or invalidate anything on disk.
* Moving the switch in either direction is a value change and nothing else. No
  schema, persisted state, cached artifact or wire identifier is keyed on the
  position, so a rollback is complete the moment the next call starts.

## 8. Exercised, not just described

`.github/workflows/executor-rollback.yml` runs this page's procedure on every
pull request: it forces `GRAPHI_CANARY_ALL=legacy`, runs the parity and
characterization suites in that position, asserts the divergence read path is
honest, and then asserts the round trip — that unsetting the variable returns
every operation to the compiled-in default, which since SW-244 the workflow
checks is **`10 shadow`**. A rollback that stopped working would fail CI rather
than fail an operator.

Since SW-248 it also runs the reachability gate (`go run ./cmd/seamreach -check`)
and then runs it again with a deliberately introduced violation — an operation in
`shadow` that no shipped profile advertises — asserting it exits **non-zero**. A
gate nobody has watched fail is a claim about a gate, and this one exists because
an absent check let exactly that defect ship.

## 9. Readiness — when an operation may leave `shadow`

Whether any operation on this seam may be moved to `active` is **computed, not
argued**:

```sh
go run ./cmd/seamready          # one verdict per operation, six criterion rows each
go run ./cmd/seamready -json    # the same as a `seam-readiness-v1` document
```

For every operation in §1 it prints `READY`, `NOT_READY` or `UNKNOWN` over the
six cutover criteria — a tagged release line in `shadow`, the divergence record
(§5) against the observation threshold `K` with zero unexplained mismatches,
argument fidelity, the performance budget, capability/provenance parity, and
this page's rollback — each row naming the artifact it rests on. Its rules are
the amended precondition (a) and its siblings in the SW-238 precondition
checklist, not a new vocabulary; the declared half of its evidence lives in
`docs/rc/seam-readiness.yaml`.

Three things to read it by:

* **`READY` is the precondition for a flip story.** The tool never moves a
  switch. An operation that prints `READY` may be flipped by a story of its own,
  which edits `TestAX14_NoMigratedOperationIsActiveByDefault` deliberately;
  nothing else changes a compiled-in position.
* **`UNKNOWN` is not "not yet".** It is the absence of an artifact — an unset
  `K`, a record with no observations, a declared test that is not in the tree —
  and it never counts as PASS. Today every operation reads `UNKNOWN` because `K`
  is unset (owner decision 1), the record holds zero observations (§5, and
  SW-248's reachability finding), **and** the performance budget (`c4`) is on
  record as UNKNOWN and blocking (SW-238 preconditions §(d): the AX-06 latency
  gate's red is routinely dismissed as runner noise, and the `test-gate` run at
  `91ee698` recorded an unwithdrawn p95 FAIL) — so no run is declared for it.
  A CI run the yaml *does* declare is checked only as "the sha is a known
  commit"; the tool cannot see a later red run of the same workflow, so a
  declared green is PASS until the yaml is edited.
* **Stable operations appear nowhere in it.** The tool evaluates
  `MigratedOperations()` and rejects a declaration that names anything else.
