"""api package — the contract that impl/ and org/ both honour.


The QN for module-level functions is `<last_dir>.<funcname>`
(`core/parse/parser_tswalk.go:240`), so the `def salute(name)` here
gives the resolved QN `api.salute` — the fixture's stable call-site
identifier for the search/callers/callees/neighborhood/impact scenarios.
"""

# SPEAKER_PREFIX is the read-only contract value every impl honours.
# The QN is `api.SPEAKER_PREFIX` and `references(SPEAKER_PREFIX)` resolves
# through impl.English.core, which reads it via the module-level name.
SPEAKER_PREFIX = "Hello "


def salute(name):
    """The contract method Speaker declares — every impl re-implements it.

    The QN is `api.salute` (KindFunction, the parser treats module-level
    defs as functions, not methods: core/parse/parser_python.go:135).
    """
    return SPEAKER_PREFIX + name
