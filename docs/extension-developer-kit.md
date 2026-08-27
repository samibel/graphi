# Extension developer kit

**Status:** Labs. Nothing on this page is a stability promise — neither the CLI
verbs, nor the manifest schema (`graphi.extension/v1alpha1`), nor the Go
harness package. Story: SW-230 (AX-10). Governing decision:
[ADR 0013 — extension trust tiers](adr/0013-extension-trust-tiers.md).

graphi has two kinds of extension you can build today, and they are different
*trust domains* rather than different amounts of permission:

| tier | what it is | who writes it | executes code? |
|---|---|---|---|
| **A — declarative rule packs** | versioned, schema-validated, SHA-256-pinned YAML/JSON | anyone | **no** |
| **B — static first-party modules** | Go, compiled into the graphi binary | graphi maintainers | it *is* graphi's code |

There is no third tier you can use. A trusted-subprocess tier (C) was spiked
(SW-231) and **decided against for this phase** —
[`docs/decisions/2026-08-process-extension-go-no-go.md`](decisions/2026-08-process-extension-go-no-go.md);
a WASM tier (D) is a decided non-goal. graphi therefore has, deliberately, **no
tier for untrusted extension code**.

Everything below is offline. `init` renders templates compiled into the binary,
`install` copies a local file, and no verb on this page opens a socket.

---

## 1. Rule packs, end to end

`graphi extension init` writes a pack that is valid on the first run — including
the artifact's SHA-256, which is the step a first-time author gets wrong.

```console-verified
$ graphi extension init --kind architecture-rules ./my-pack
$ graphi extension lint ./my-pack
$ graphi extension conform ./my-pack
$ graphi extension validate ./my-pack/pack.yaml
```

Four files land:

| file | what it is |
|---|---|
| `pack.yaml` | the manifest — identity, kind, host API range, the artifact's hash, the capability keys it provides, its permission and its limits |
| `rules.yaml` / `taint.yaml` | the data the pack contributes, one real example rule |
| `pack_test.go` | the conformance harness as a three-line Go test |
| `README.md` | the loop above, and the sentence about what a pack is allowed to be |

Then edit, re-pin, re-check:

```console
$ $EDITOR ./my-pack/rules.yaml
$ shasum -a 256 ./my-pack/rules.yaml      # paste into artifact.sha256
$ graphi extension lint ./my-pack
$ graphi extension install --sha256 <hash-from-validate> ./my-pack/pack.yaml
```

`--sha256` is **mandatory** on install and is verified before a byte is written.
Signing is deferred, so the pin proves *"the same bytes as when you installed
it"*, not *"the bytes the author intended"* — install packs you would trust with
a pull request.

### `lint` vs `validate`

They answer different questions and both are useful.

- **`validate`** answers *would this install?* the way installation does:
  fail-closed, first violation, non-zero exit. It is the gate.
- **`lint`** answers *what is wrong with my pack?* It reports **every** problem
  it can, in **both** files, each located:

```console
$ graphi extension lint ./my-pack
my-pack/pack.yaml:34:1: extpack: determinism "whenever" is not accepted: a declarative pack is "deterministic"
my-pack/pack.yaml:37:3: extpack: limits.max_output_bytes must be a positive byte count
my-pack/rules.yaml:8:5: extpack: architecture rule "ui-must-not-reach-storage" from is empty
3 problem(s)
```

`validate` is the head of exactly the list `lint` prints, so the two can never
disagree about whether a pack is valid — only about how much they say.

### Exit codes

| code | meaning |
|---|---|
| `0` | the operation succeeded |
| `1` | an actionable no: the pack did not validate, a hash did not match, a check failed |
| `2` | a usage error: unknown subcommand, missing argument, missing `--sha256` |

`1` and `2` are kept apart on purpose: a caller that cannot tell them apart
retries the wrong one.

### What a pack can never be

Each of these is pinned by an attack test, not by this paragraph:

- It cannot name a file to read — `artifact.path` is a bare filename, resolved
  once, next to the manifest, and never used as a path again. There is no URL
  field and none can be added without a schema version bump, because unknown
  manifest fields are **rejected**.
- It cannot exceed the size it declared, or declare its way past the host ceiling.
- It cannot shadow a built-in or another pack. A pack **adds** capability keys;
  it never takes one (`registry.ErrUnsupportedOverride`).
- It cannot ask for a permission tier A does not have. `graph:read` is the only
  value the schema accepts — there is no network, filesystem or exec permission
  to request.
- It cannot raise anyone's confidence. Every pack-derived item carries the pack's
  id, version and hash, and the ceiling for an extension-influenced claim is
  `derived`; `confirmed` is closed to extensions.

Disabling a pack restores **byte-identical** pre-pack behaviour. That is a
tested contract, not an expectation.

---

## 2. The conformance harness

`engine/extpack/conformance` is the importable Go package; `graphi extension
conform` is the same checks without writing Go.

### For a pack

```go
report := conformance.VerifyPack("./my-pack")
if err := report.Err(); err != nil {
    t.Fatal(err)
}
```

| check | what it proves |
|---|---|
| `manifest` | the manifest validates, with every diagnostic located |
| `artifact-schema` | the artifact validates against its kind's schema |
| `api-version` | this host's rule-pack API version is inside the declared range |
| `merge-determinism` | two independent installs merge to **identical bytes** |
| `provenance` | every merged rule carries the pack's id, version and hash |

### For an operation contribution

```go
report := conformance.VerifyContribution(ctx, conformance.Contribution{
    Spec:     spec,                       // engine/opcatalog.OperationSpec
    API:      extpack.APIRange{Min: conformance.HostAPIVersion, Max: conformance.HostAPIVersion},
    Ports:    conformance.Ports{opcatalog.PortGraphQuery: querySvc},
    Invoke:   handler,                    // func(ctx, Host, json.RawMessage) ([]byte, error)
    Fixtures: []conformance.Fixture{{Name: "default"}},
})
```

| check | what it proves |
|---|---|
| `spec` | the spec is valid, complete, and has a handler and fixtures behind it |
| `permissions` | the ports imply only read-only permissions (ADR 0013 I2/I3) |
| `api-version` | an out-of-range `api` is **rejected**, not resolved to something nearby |
| `surface-metadata` | the spec carries everything a surface projection reads |
| `surface-projection` | the real MCP/HTTP projections render valid metadata *for this operation* |
| `determinism` | the same fixture inputs produce the same bytes, twice |
| `port-honesty` | the handler touched **exactly** the ports it declared |

Port honesty is enforced through a gate rather than by inspection. A handler
reaches every port by name through `Host.Use`, and the gate **records the
attempt before it answers** — so a handler that swallows the refusal and carries
on still fails the check. Over-declaration fails too: the permission set a user
is asked to grant is derived from the port list, so a port declared and never
used over-states what the contribution takes.

### The harness can fail, and that is the point

`engine/extpack/conformance/conformance_selftest_test.go` proves every check in
both directions. An honest control passes; a deliberately non-deterministic
contribution and one that reaches for an undeclared port **must fail**, on the
specific check that exists to catch them. A harness that has never been observed
failing certifies nothing.

---

## 3. Worked example — `dead_code` as a module

**The claim:** an existing read-only built-in is expressible as *one module
directory, one registration and harness tests*, with **zero manual dispatch or
descriptor edits**.

**The operation:** `dead_code`. It was chosen because it satisfies the harness's
own criteria rather than needing an exemption from them — catalog tier `labs`,
determinism `deterministic`, ports `[graph.query, graph.search]`, permissions
`[graph.read]` only — and because both halves of the claim are *observable* for
it: its MCP descriptor is already projected from its catalog spec (SW-225), and
its surface dispatch already reaches the generic executor (SW-226).

**The registration**, in full:

```go
set := module.NewSet()
_ = set.Add(module.Module{
    Manifest: module.Manifest{ID: "example.deadcode", Version: "1"},
    Register: func(b *module.Builder) error { return b.AddOperation(spec) },
})
composition, _ := set.Build(module.Inputs{Reader: reader})
```

That is the whole wiring. The spec is *read from the operation catalog*, not
retyped — a worked example that restated the operation's metadata would be
demonstrating a second source of truth, which is the thing the catalog exists to
remove.

**What the test asserts** (`surfaces/ax10_worked_example_test.go`):

1. The module registration alone produces the catalog entry.
2. The descriptor the live MCP server advertises in `tools/list` is
   **byte-identical** to the projection of that registered spec — so a descriptor
   edit is not merely unnecessary, it would be *detected*.
3. The live HTTP `/contract` advertises the operation at the resource the spec
   declares, and the **default** profile still does not (a Labs operation must
   not widen the frozen surface).
4. With the executor kill switch at `active`, the MCP call is answered entirely
   through the generic executor and the bytes do not move.

Plus the harness itself, run against the **real** MCP and HTTP projections
rather than stubs — the one place in the tree where that happens, because a
harness pointed at a re-implementation certifies the re-implementation.

**What is not yet true.** `engine/module`'s built-in set still contributes the
whole catalog in one module (`engine.operations`), so a *first-party* operation
is not yet one module each. Splitting that is SW-228 and SW-232 work. What AC-4
establishes is that the seam supports it: the registration above is the entire
delta, and nothing in `surfaces/mcp/toolcalls.go` or
`surfaces/mcp/descriptors.go` had to move.

---

## 4. Not in scope here

- **No process-extension SDK.** Tier C was spiked (SW-231) and the decision is
  **no-go** for this phase — a planned, valid outcome, recorded in
  [`docs/decisions/2026-08-process-extension-go-no-go.md`](decisions/2026-08-process-extension-go-no-go.md)
  with the measurements behind it. If it is ever reopened, every surface that offers one must
  say, in these words, that a subprocess extension is **trusted local code — the
  same category as a shell script you chose to run** — not a sandbox. A
  permission manifest bounds host-API access and makes it transparent; it does
  not constrain another process's syscalls.
- **No template registry and no downloads.** `init` is entirely local.
- **No API stability promise** for `engine/extpack/conformance`. It is Labs and
  may change shape.

## See also

- [ADR 0013 — extension trust tiers](adr/0013-extension-trust-tiers.md)
- [`docs/coverage-matrix.md`](coverage-matrix.md) — the `extension` row
- [`docs/stability-tiers.md`](stability-tiers.md) — what Labs means here
