# Qrel-blind bundle-sufficiency grading rubric

Grade one response using only its generated grader packet. Return `PASS` only
when the response directly answers the question, all material claims are
supported by the preserved bundle, the essential reviewed grade-3 behavior is
present, any citation resolves inside the bundle, and no material contradiction
or invented repository fact appears. Otherwise return `FAIL`.

The only valid raw formats are:

```text
PASS: <specific bundle-grounded rationale>
FAIL: <specific bundle-grounded rationale>
```

Do not consult the repository, another response, prior evaluation material, or
outside knowledge. A missing, empty, refused, conditional, probabilistic, or
unsupported answer does not pass. When the necessary fact is absent from the
bundle, return `FAIL`.
