#!/usr/bin/env python3
"""Fetch the allowed Cobra issue text for the SW-279 population, and nothing else.

Section 1 of the frozen inclusion rule permits exactly four pieces of issue content for
selection: the author's title, the opening body, the immutable issue number, the author,
and the creation time. It forbids fetching labels, reactions, comments, maintainer
replies, linked pull requests, closing events, and external links.

That prohibition is a transport prohibition (see
projects/graphi/stories/SW-279/decision-transport-overfetch.md), so the compliance
argument has to live in a selection set a reviewer can read, not in a sentence an actor
wrote afterwards. The selection set below is the whole of it:

    nodes { number createdAt title body author { login } }

A REST issue response cannot express this: it always carries labels, state, assignees,
milestone and reactions. GraphQL selects fields, so it can.

The result is written to disk. It is never printed. The actor that classifies these rows
reads the file and never holds the API response.
"""

from __future__ import annotations

import hashlib
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


OUT_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RULE_PATH = Path("docs/eval/retrieval/dataset-v2-inclusion-rule.md")
ACTOR = "Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)"

# The literal selection set, quoted into the access ledger so the ledger's claim and the
# program's behaviour are the same string.
#
# It used to be *only* quoted into the ledger: the executed query carried its own copy of
# the same field list, so adding `labels` to the query without touching this constant would
# have recreated the exact overfetch this story exists to prevent while the ledger went on
# certifying "and nothing else". The two are bound now in both directions - the query is
# built from this constant, and `assert_query_is_the_selection_set` re-derives the executed
# query's issue-connection body and refuses if it is anything but this constant plus paging.
SELECTION_SET = "nodes { number createdAt title body author { login } }"

# Paging is not issue content: `pageInfo` selects cursor metadata about the connection, not
# fields of any issue. It is named separately so the assertion below can allow it explicitly
# rather than tolerating whatever else happens to be there.
PAGE_INFO = "pageInfo { hasNextPage endCursor }"

# The repository this harvest is permitted to read, and the only one. The metadata records
# `spf13/cobra`; these two constants are where that claim comes from, they are what the
# variables are bound to at the call site, and `assert_variables_pin_the_repository` refuses
# any other binding before the first request.
REPO_OWNER = "spf13"
REPO_NAME = "cobra"

# The argument lists the operation is permitted to carry, declared once and interpolated into
# the query below, so - as with SELECTION_SET - the constant the checks compare against and
# the text that is actually sent are the same string.
#
# Round 3 found `parse_selection_set` skipping every balanced `( ... )` without ever looking
# inside it, so `repository(owner: "kubernetes", name: "kubernetes")` passed the whole
# assertion: the field names were right, the selection set was right, and the fetcher would
# have written another repository's issue bodies into a file whose metadata says
# `spf13/cobra`. Arguments are compared now, by token equality (whitespace and commas are
# insignificant to GraphQL and so are insignificant here), everywhere they can appear:
# the operation's variable definitions, `repository`, and `issues`. Nothing under `issues`
# can carry an argument unnoticed either, because that whole selection body is compared to
# SELECTION_SET + PAGE_INFO by equality.
#
# `after: $cursor` is the one argument whose *value* legitimately varies: the fetcher walks
# the connection. It varies as a variable binding at request time, never as a change to the
# operation - which is why the operation is pinned by equality here while
# `assert_variables_pin_the_repository` pins `owner` and `name` to the constants above and
# leaves `cursor` free.
VARIABLE_DEFINITIONS = "$owner: String!, $name: String!, $cursor: String"
REPOSITORY_ARGS = "owner: $owner, name: $name"
ISSUES_ARGS = """
      first: 50
      after: $cursor
      states: [OPEN, CLOSED]
      orderBy: {field: CREATED_AT, direction: ASC}
    """

QUERY_TEMPLATE = """
query(%(variable_definitions)s) {
  repository(%(repository_args)s) {
    issues(%(issues_args)s) {
      %(selection_set)s
      %(page_info)s
    }
  }
}
"""


def normalise(fragment: str) -> str:
    """Collapse GraphQL whitespace so two spellings of one selection compare equal."""
    return " ".join(fragment.replace("{", " { ").replace("}", " } ").split())


# GraphQL lexical tokens. Commas, whitespace and `#` comments are insignificant and are
# dropped by `lex`; everything else must match one of these, so a character the lexer does
# not understand is a refusal rather than something silently skipped over.
TOKEN = re.compile(
    r'\.\.\.'                              # spread, for fragments
    r'|"""(?:.|\n)*?"""'                    # block string
    r'|"(?:[^"\\\n]|\\.)*"'                # string
    r'|\$?[_A-Za-z][_0-9A-Za-z]*'           # name or variable
    r'|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?'   # number
    r'|[!$()\[\]{}:=@|&]'                   # punctuation
)
NAME = re.compile(r"[_A-Za-z][_0-9A-Za-z]*\Z")


class Overfetch(SystemExit):
    """Raised when the operation about to be sent is not the one §1 permits."""


def refuse(reason: str, query: str) -> None:
    raise Overfetch(
        "the executed GraphQL operation is not the declared one; §1 permits the issue "
        "number, creation time, title, body and author login and nothing else.\n"
        f"  problem: {reason}\n"
        f"  operation:\n{query.strip()}"
    )


def lex(query: str) -> list[tuple[str, int, int]]:
    """Return (token, start, end) triples, dropping insignificant commas and comments."""
    tokens: list[tuple[str, int, int]] = []
    i = 0
    while i < len(query):
        char = query[i]
        if char in " \t\r\n\ufeff,":
            i += 1
            continue
        if char == "#":
            newline = query.find("\n", i)
            i = len(query) if newline < 0 else newline + 1
            continue
        match = TOKEN.match(query, i)
        if match is None:
            refuse(f"unlexable character {char!r} at offset {i}", query)
        tokens.append((match.group(0), match.start(), match.end()))
        i = match.end()
    tokens.append(("", len(query), len(query)))  # sentinel, so no walk runs off the end
    return tokens


def tokens_of(fragment: str) -> str:
    """Collapse a GraphQL fragment to its significant tokens, space-separated.

    Whitespace, commas and comments are insignificant to GraphQL, and `lex` already drops
    them, so two spellings of one argument list compare equal here while any change to what
    is being asked for does not. The trailing sentinel is dropped.
    """
    return " ".join(token for token, _, _ in lex(fragment)[:-1])


class Field:
    """One field in a selection set, with its arguments, its parsed sub-selection and that
    sub-selection's span in the original source, so the bytes that will be sent are what gets
    compared."""

    def __init__(self, name: str, alias: str | None, args: str | None,
                 fields: list["Field"] | None, span: tuple[int, int] | None) -> None:
        self.name = name
        self.alias = alias
        self.args = args      # significant tokens of `( ... )`, or None when there is no list
        self.fields = fields  # None when the field has no selection set
        self.span = span      # (offset just after `{`, offset of the matching `}`)


def skip_balanced(tokens: list[tuple[str, int, int]], i: int, opener: str, closer: str,
                  query: str) -> int:
    depth = 0
    while i < len(tokens):
        value = tokens[i][0]
        if value == opener:
            depth += 1
        elif value == closer:
            depth -= 1
            if depth == 0:
                return i + 1
        elif value == "":
            break
        i += 1
    refuse(f"unterminated {opener}{closer}", query)
    return i  # unreachable; refuse raises


def parse_selection_set(tokens: list[tuple[str, int, int]], i: int,
                        query: str) -> tuple[list[Field], int]:
    """Parse `{ ... }` starting at tokens[i], returning its fields and the index after `}`."""
    if tokens[i][0] != "{":
        refuse("expected a selection set", query)
    open_at = tokens[i][2]
    i += 1
    fields: list[Field] = []
    while tokens[i][0] != "}":
        value = tokens[i][0]
        if value == "":
            refuse("unterminated selection set", query)
        if value == "...":
            refuse("a fragment spread or inline fragment can smuggle any field past a "
                   "field-name check, so neither is allowed anywhere in this operation", query)
        if not NAME.match(value):
            refuse(f"expected a field name, found {value!r}", query)
        alias: str | None = None
        name = value
        i += 1
        if tokens[i][0] == ":":
            # `alias: field` - the wire name is the alias, so a check that reads only field
            # names would not see what is actually being transported.
            alias, name = name, tokens[i + 1][0]
            i += 2
        args: str | None = None
        if tokens[i][0] == "(":
            args_from = tokens[i][2]
            i = skip_balanced(tokens, i, "(", ")", query)
            args = tokens_of(query[args_from:tokens[i - 1][1]])
        if tokens[i][0] == "@":
            refuse("a directive can change what is returned, so none is allowed", query)
        sub: list[Field] | None = None
        span: tuple[int, int] | None = None
        if tokens[i][0] == "{":
            start = tokens[i][2]
            sub, i = parse_selection_set(tokens, i, query)
            span = (start, tokens[i - 1][1])  # tokens[i-1] is the matching `}`
        fields.append(Field(name, alias, args, sub, span))
    del open_at
    return fields, i + 1


def only_field(fields: list[Field], expected: str, where: str, query: str) -> Field:
    """Refuse unless `fields` is exactly one un-aliased field named `expected`."""
    if len(fields) != 1:
        found = ", ".join(f"{f.alias + ': ' if f.alias else ''}{f.name}" for f in fields)
        refuse(f"{where} selects {len(fields)} fields ({found}); §1 permits exactly one "
               f"({expected}), because any sibling selection transports issue content the "
               "issues-connection check never looks at", query)
    field = fields[0]
    if field.alias is not None:
        refuse(f"{where} selects an aliased field {field.alias}: {field.name}; an alias hides "
               "the field actually being requested", query)
    if field.name != expected:
        refuse(f"{where} selects {field.name}, not {expected}", query)
    if field.fields is None or field.span is None:
        refuse(f"{where}'s {expected} has no selection set", query)
    return field


def only_args(field: Field, declared: str, where: str, query: str) -> None:
    """Refuse unless `field` carries exactly the declared argument list.

    Round 3's finding: the parser skipped every balanced `( ... )` without comparing it, so
    `repository(owner: "kubernetes", name: "kubernetes")` passed a check that reads only field
    names - the fetch would have written a different repository's issue bodies into a file
    whose metadata says `spf13/cobra`. Equality, not containment: an added argument, a removed
    one and a changed one are all changes to what is being asked for.
    """
    expected = tokens_of(declared)
    if field.args is None:
        refuse(f"{where} carries no argument list; §1's transport is `{expected}`", query)
    if field.args != expected:
        refuse(f"{where}'s arguments are not the declared ones\n"
               f"  declared: {expected}\n"
               f"  executed: {field.args}", query)


def assert_variables_pin_the_repository(variables: dict[str, str], query: str = None) -> None:
    """Refuse unless the variable bindings name the one repository §1 permits.

    The operation asks for `repository(owner: $owner, name: $name)`, so pinning the operation
    pins the *shape* and leaves the target to the bindings sent alongside it. This is the other
    half: `owner` and `name` must be exactly REPO_OWNER and REPO_NAME, and no other variable
    may be bound here. `cursor` is not passed through this dict - paging is the one thing that
    legitimately varies, and it is added per request by the caller.
    """
    expected = {"owner": REPO_OWNER, "name": REPO_NAME}
    if variables != expected:
        refuse(
            "the repository variables are not the declared ones; the metadata and the access "
            f"ledger record `{REPO_OWNER}/{REPO_NAME}`, so any other binding fetches issue "
            "bodies this harvest has no permission to read\n"
            f"  declared: {expected}\n"
            f"  bound:    {variables}",
            QUERY if query is None else query,
        )


def assert_query_is_the_selection_set(query: str = None) -> str:
    """Refuse unless the WHOLE operation is the one §1 permits.

    Round 1 bound the `issues(...)` body to `SELECTION_SET`, which was not enough: the check
    read one selection body and ignored the rest of the document, so a sibling
    `issue(number: 1) { labels { nodes { name } } }` under the same `repository` passed while
    transporting labels. This validates the whole operation instead:

      * exactly one operation definition, a `query`, with nothing after it;
      * no fragment definition, no fragment spread, no inline fragment, no directive - each
        of which can put a field into the response without that field's name appearing where
        a naive check looks;
      * no alias anywhere on the path, so the field being requested is the field being named;
      * `repository` is the ONLY field at the root and `issues` is the ONLY field under it;
      * every argument list in the operation - the variable definitions, `repository`'s and
        `issues`' - is exactly the declared one, by token equality. Round 3 found these
        skipped without comparison, which let `repository(owner: "kubernetes", ...)` through
        a check that had already approved every field name in the document;
      * the `issues` selection is exactly SELECTION_SET plus paging, by equality.

    Returns the normalised issues-connection body it verified, so a caller can record what it
    checked.
    """
    if query is None:
        query = QUERY

    tokens = lex(query)
    for value, _, _ in tokens:
        if value == "fragment":
            refuse("a fragment definition can carry any field list; none is allowed", query)

    i = 0
    head = tokens[0][0]
    if head in {"mutation", "subscription"}:
        refuse(f"this is a {head}, not a query", query)
    if head != "query":
        refuse(f"expected a query operation, found {head!r}", query)
    i = 1
    if NAME.match(tokens[i][0]):
        i += 1  # an operation name is permitted; it names the operation, not a field
    if tokens[i][0] != "(":
        # The declared transport takes its repository from variables, so an operation that
        # declares none is either taking it from a literal or from a default - both of which
        # are changes to what is being asked for.
        refuse("the operation declares no variables; §1's transport declares "
               f"`{tokens_of(VARIABLE_DEFINITIONS)}`", query)
    # A variable definition can carry a default value, so it is part of what the operation
    # asks for and is pinned like any other argument list.
    definitions_from = tokens[i][2]
    i = skip_balanced(tokens, i, "(", ")", query)
    definitions = tokens_of(query[definitions_from:tokens[i - 1][1]])
    if definitions != tokens_of(VARIABLE_DEFINITIONS):
        refuse("the operation's variable definitions are not the declared ones\n"
               f"  declared: {tokens_of(VARIABLE_DEFINITIONS)}\n"
               f"  executed: {definitions}", query)

    root, i = parse_selection_set(tokens, i, query)
    if tokens[i][0] != "":
        refuse("there is more than one definition in this document; a second operation or a "
               "trailing fragment is not part of the declared transport", query)

    repository = only_field(root, "repository", "the operation root", query)
    only_args(repository, REPOSITORY_ARGS, "repository", query)
    issues = only_field(repository.fields, "issues", "repository", query)
    only_args(issues, ISSUES_ARGS, "issues", query)

    # The body is the source span the parser itself walked, not a `str.index` for `issues(`,
    # so a sibling field whose name merely ends in `issues` cannot be read in its place.
    body = normalise(query[issues.span[0]:issues.span[1]])
    expected = normalise(SELECTION_SET + " " + PAGE_INFO)
    if body != expected:
        refuse(f"the issues(...) selection does not match the declared selection set\n"
               f"  declared: {expected}\n"
               f"  executed: {body}", query)
    return body


QUERY = QUERY_TEMPLATE % {
    "variable_definitions": VARIABLE_DEFINITIONS,
    "repository_args": REPOSITORY_ARGS,
    "issues_args": ISSUES_ARGS,
    "selection_set": SELECTION_SET,
    "page_info": PAGE_INFO,
}

# The repository variables, bound once here and sent verbatim, so what
# `assert_variables_pin_the_repository` checks is what the request carries. `cursor` is not
# in this dict: paging is the one binding that varies, and it is added per request.
VARIABLES = {"owner": REPO_OWNER, "name": REPO_NAME}


def run(*args: str) -> str:
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True).stdout.strip()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def main() -> int:
    root = Path(run("git", "rev-parse", "--show-toplevel"))
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    # Before anything reaches the network: the query that will actually be sent asks for
    # SELECTION_SET and paging, and nothing else. This raises SystemExit rather than
    # returning, because a divergence here means the ledger is about to certify a
    # transport that is not the one being used.
    verified_selection = assert_query_is_the_selection_set()

    # And the operation's target: the query pins the shape `repository(owner: $owner, name:
    # $name)`, this pins what those two variables carry. Both halves are needed - an operation
    # whose arguments are all correct still reads whatever repository the bindings name.
    assert_variables_pin_the_repository(VARIABLES)

    # The Phase 1 boundary is re-verified here too: this fetch must postdate the frozen
    # rule commit and the working rule bytes must still be the committed bytes.
    committed_rule = subprocess.run(
        ["git", "show", f"{RULE_COMMIT}:{RULE_PATH.as_posix()}"],
        check=True, stdout=subprocess.PIPE,
    ).stdout
    if RULE_PATH.read_bytes() != committed_rule:
        print("working rule bytes differ from the frozen commit", file=sys.stderr)
        return 2
    commit_time = parse_time(run("git", "show", "-s", "--format=%cI", RULE_COMMIT))
    fetch_start = datetime.now(timezone.utc)
    if fetch_start <= commit_time:
        print("fetch start is not later than the frozen rule commit", file=sys.stderr)
        return 2

    manifest_path = OUT_DIR / "issue-numbers.txt"
    raw_path = OUT_DIR / "issue-text-raw.jsonl"
    meta_path = OUT_DIR / "issue-text-raw-metadata.json"
    ledger_path = OUT_DIR / "access-ledger.jsonl"
    for path in (raw_path, meta_path):
        if path.exists():
            print(f"refusing to overwrite frozen artifact: {path}", file=sys.stderr)
            return 2

    manifest_bytes = manifest_path.read_bytes()
    manifest_digest = sha256(manifest_bytes)
    numbers = [int(line) for line in manifest_bytes.decode("ascii").splitlines()]
    wanted = set(numbers)
    if len(wanted) != len(numbers):
        print("duplicate issue number in the manifest", file=sys.stderr)
        return 2

    cursor: str | None = None
    pages = 0
    nodes_seen = 0
    by_number: dict[int, dict[str, object]] = {}
    while True:
        command = ["gh", "api", "graphql", "-f", f"query={QUERY}"]
        for key, value in VARIABLES.items():
            command.extend(["-F", f"{key}={value}"])
        if cursor is not None:
            command.extend(["-F", f"cursor={cursor}"])
        payload = json.loads(run(*command))
        if payload.get("errors"):
            raise RuntimeError(payload["errors"])
        issues = payload["data"]["repository"]["issues"]
        pages += 1
        for node in issues["nodes"]:
            nodes_seen += 1
            number = int(node["number"])
            if number not in wanted:
                continue  # created after the frozen cutoff; not in the population
            if number in by_number:
                print(f"duplicate issue node for {number}", file=sys.stderr)
                return 2
            author = node.get("author")
            by_number[number] = {
                "issue_number": number,
                "author": (author or {}).get("login"),
                "created_at": node.get("createdAt"),
                "title": node.get("title"),
                "body": node.get("body"),
            }
        page_info = issues["pageInfo"]
        if not page_info["hasNextPage"]:
            break
        cursor = page_info["endCursor"]

    missing = sorted(wanted - set(by_number))
    if missing:
        print(f"manifest issues absent from the fetch: {missing}", file=sys.stderr)
        return 2

    rows = [by_number[number] for number in numbers]

    # created_at was null in 1,255 of 1,255 rows of the superseded archive. Section 1
    # permits creation time, the population cutoff is defined on it, and without it the
    # population boundary is not re-derivable from the archive. Stop rather than repeat it.
    null_created = [row["issue_number"] for row in rows if not row["created_at"]]
    if null_created:
        print(f"created_at is null for {len(null_created)} rows; refusing to write", file=sys.stderr)
        return 2
    null_titles = [row["issue_number"] for row in rows if row["title"] is None]
    if null_titles:
        print(f"title is null for {len(null_titles)} rows; refusing to write", file=sys.stderr)
        return 2
    off_cutoff = [row["issue_number"] for row in rows if parse_time(str(row["created_at"])) > commit_time]
    if off_cutoff:
        print(f"rows created after the cutoff: {off_cutoff}", file=sys.stderr)
        return 2

    raw_bytes = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )
    raw_path.write_bytes(raw_bytes)
    raw_digest = sha256(raw_bytes)
    fetch_end = datetime.now(timezone.utc)
    stamp = lambda value: value.isoformat(timespec="microseconds").replace("+00:00", "Z")

    metadata = {
        "schema": "sw-279-raw-issue-text/v1",
        "repository": f"{REPO_OWNER}/{REPO_NAME}",
        "population_manifest_file": manifest_path.name,
        "population_manifest_sha256": manifest_digest,
        "row_count": len(rows),
        "row_order": "ascending issue_number, exactly matching issue-numbers.txt",
        "graphql_selection_set": SELECTION_SET,
        "graphql_issues_connection_body_as_executed": verified_selection,
        "requested_fields": ["number", "createdAt", "title", "body", "author.login"],
        "explicitly_not_requested": [
            "labels", "reactions", "comments", "timelineItems", "closedAt", "stateReason",
            "state", "assignees", "milestone", "projectCards", "linkedBranches",
            "participants", "userContentEdits", "reactionGroups",
        ],
        "external_link_targets_opened": 0,
        "created_at_null_count": 0,
        "title_null_count": 0,
        "body_null_count": sum(1 for row in rows if row["body"] is None),
        "author_null_count": sum(1 for row in rows if row["author"] is None),
        "raw_file": raw_path.name,
        "raw_sha256": raw_digest,
        "github_pages_fetched": pages,
        "github_issue_nodes_seen_including_post_cutoff": nodes_seen,
        "fetch_started_at_utc": stamp(fetch_start),
        "fetch_completed_at_utc": stamp(fetch_end),
        "fetched_by": ACTOR,
        "printed_to_operator_console": False,
    }
    meta_path.write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        ledger_path,
        actor=ACTOR,
        command_tool_class="gh api graphql: field-selective allowed issue-text fetch",
        input_artifact=manifest_path.as_posix(),
        input_sha256=manifest_digest,
        output_artifact=raw_path.as_posix(),
        output_sha256=raw_digest,
        detail=(
            "GraphQL selection set was exactly `" + SELECTION_SET + "` and nothing else — "
            "not asserted, but re-derived from the query that was sent and compared to that "
            "constant before the first request (`assert_query_is_the_selection_set`), which "
            "read back `" + verified_selection + "`. "
            "No label, reaction, comment, maintainer reply, timeline item, closing event, "
            "state, assignee, milestone, or linked pull request was requested, so none was "
            "transported. No external link target was opened. The response was written to "
            "disk by this program and was never printed to an operator console."
        ),
        timestamp_utc=stamp(fetch_end),
    )

    # Counts only. No title or body value is printed.
    print(json.dumps({
        "row_count": metadata["row_count"],
        "population_manifest_sha256": manifest_digest,
        "raw_sha256": raw_digest,
        "created_at_null_count": 0,
        "body_null_count": metadata["body_null_count"],
        "author_null_count": metadata["author_null_count"],
        "pages": pages,
        "nodes_seen": nodes_seen,
        "selection_set": SELECTION_SET,
    }, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
