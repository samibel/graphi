"""app package — the second cross-module caller.

Like Service.py, this wires the same import form. The two-callers-of-core
shape is what gives the callers and impact scenarios their cross-file
reach.
"""

from impl import core


def run(name):
    """A second cross-module caller of core.

    The QN is `app.run`. The callers-of-core scenario asserts this as a
    positive anchor (alongside app.serve, app.twice, and impl.salute).
    """
    return core(name)
