"""impl package — the canonical implementation of the Speaker contract.

QN summary (langPackage-derived, core/parse/parser_tswalk.go:240):
  - impl.core       (the helper every caller in app/ uses)
  - impl.salute     (the contract method, calls core)
  - impl._format    (the helper `core` calls, the references target)

The from-import bare binding in app/Service.py and app/Report.py wires
the heuristic cross-module edge: `from impl import core` — the G2SUB
heuristic resolver's primary synthesis path. The same QN is reused
across both forms, so the heuristic tier is asserted by the witness at
this level, never re-affirmed at confirmed.
"""


def _format(prefix, name):
    """The internal helper `core` calls — the references target.

    The QN is `impl._format`. The references operation finds `impl.core`
    as a referrer (the only calls ref `_format` in the fixture), so the
    references scenario can anchor on this surface WITHOUT crossing
    modules. The Python parser does NOT track in-function variable
    reads (it only tracks `call` nodes: core/parse/parser_python.go:200),
    so a module-level constant cannot be the references target — a
    function QN is the smallest shape that exercises the operation.
    """
    return prefix + name


def core(name):
    """The shared callee every caller pivots on.

    The witness scenarios (callers, callees, impact, neighborhood,
    related_files) all anchor on `impl.core`. Calls `impl._format`,
    which yields the `references` edge.
    """
    return _format("Hi ", name)


def salute(name):
    """English honours the Speaker contract by delegating to core().

    QN is `impl.salute`. The callers-of-core scenario asserts this as a
    positive (salute calls core); the callers-of-salute scenario asserts
    it as ambiguous (api.salute, impl.salute, org.salute all share the
    name).
    """
    return core(name)
