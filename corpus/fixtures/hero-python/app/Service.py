"""app package — the cross-module caller the heuristic resolver wires.

The from-import bare binding here is the G2SUB heuristic resolver's
primary synthesis path. The import path keys on the LAST directory,
matching langPackage (`impl/English.py` → `impl`), so the import
`from impl import core` resolves the bare binding to the heuristic
edge `app.serve -> impl.core`.

The QN keys on the LAST package directory of the file path
(`core/parse/parser_tswalk.go:240`), so the import target is `impl.core`
and the resolver emits a `calls` edge `app.serve -> impl.core` at the
heuristic tier — never at confirmed, because Python has no typed binder.
"""

from impl import core


def serve(name):
    """The cross-module caller of core: emits a heuristic edge to impl.core.

    The QN is `app.serve`. The callers-of-core scenario asserts this as a
    positive anchor; the callees-of-serve scenario asserts `impl.core` as a
    positive.
    """
    return core(name)


def twice(name):
    """The richer callee shape: twice calls core twice, so the popped
    Witnesses for callees (callers-of-twice) and for callees-of-twice
    pivot on this name.

    The QN is `app.twice`. The callees-of-twice scenario asserts
    `impl.core` as a positive anchor.
    """
    return core(name) + core("b")
