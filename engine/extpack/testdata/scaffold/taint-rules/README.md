# example.taint-rules

A graphi rule pack of kind `taint-rules`, scaffolded by `graphi extension init`.
It declares one taint source, one sink and one sanitizer — edit `taint.yaml` to make it yours.

**A pack is data.** graphi executes nothing this pack ships and follows no path
or URL it names. The only permission a declarative pack can hold is
`graph:read`, and the schema cannot express any other.

## The loop

```console
# 1. Edit the artifact.
$ $EDITOR taint.yaml

# 2. Re-pin it — every edit changes the hash the manifest carries.
$ shasum -a 256 taint.yaml

# 3. See every problem at once, with line numbers.
$ graphi extension lint .

# 4. Prove the contract: schema, api compatibility, deterministic merge,
#    provenance on every merged rule.
$ graphi extension conform .

# 5. Check what would install, and print the hash to pin it with.
$ graphi extension validate pack.yaml

# 6. Install into a repository. Offline, and --sha256 is mandatory.
$ graphi extension install --sha256 <hash-from-step-5> pack.yaml
```

`graphi extension disable example.taint-rules` restores byte-identical pre-pack
behaviour; `graphi extension remove example.taint-rules` deletes it.

## Files

| file | what it is |
|---|---|
| `pack.yaml` | the manifest, schema `graphi.extension/v1alpha1` |
| `taint.yaml` | the data this pack contributes |
| `pack_test.go` | the conformance harness, as a Go test |
| `README.md` | this file |
