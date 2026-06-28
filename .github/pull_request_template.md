<!-- Thanks for contributing to graphi! Please fill out the sections below. -->

## Summary

<!-- What does this PR change, and why? -->

## Related issues

<!-- e.g. Closes #123 -->

## How was this verified?

<!-- Commands you ran, tests added, manual checks. -->

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
```

## Checklist

- [ ] Builds and tests pass under `CGO_ENABLED=0`
- [ ] `gofmt`/`go vet` clean; diff is minimal and matches surrounding style
- [ ] Local-first contract preserved (no new runtime egress; default stays CGo-free)
- [ ] Layered imports respected (`cmd → surfaces → engine → core`)
- [ ] Coverage matrix / docs updated if a parser, analyzer, MCP tool, or surface changed
- [ ] Tests added or updated for the change
