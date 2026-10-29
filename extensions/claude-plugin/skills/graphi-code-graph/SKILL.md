---
name: graphi-code-graph
description: Answer code-structure questions with the graphi code graph (MCP tools or the `graphi` CLI) instead of grepping the repository - where a symbol is defined, who calls or references it, what depends on it, what breaks if it changes. Use before exploring an indexed repo, and whenever a graphi-shunt hook redirects a search.
---

# Answering structure questions with graphi

graphi indexes the repository once into a graph: symbols are nodes, calls /
references / imports are edges, and every edge carries a `file:line` citation and
a confidence tier. One graph call replaces a grep-and-read loop and names the
edge it arrived by.

## Graph or grep

| Question | Use |
| --- | --- |
| Where is `X` defined? | `definition` / `search` |
| Who calls or uses `X`? | `callers` / `references` |
| What does `X` depend on? | `callees` / `neighborhood` |
| What breaks if I change `X`? | `impact`, then `change_risk` |
| Where do I start on this task? | `agent_brief`, then `related_files` |
| Log messages, error strings, config values, comments, TODOs | **grep is correct** |

graphi answers about symbols the repository defines. Standard-library and
third-party targets are recorded but not navigable, and text that is not a symbol
is not in the graph at all - use grep for those.

## Workflow for a task

1. `agent_brief` with the task phrase: a compact orientation.
2. `related_files` with the same phrase (or a symbol): the files that matter.
3. `explain_symbol` on each symbol you are about to read: usually enough to skip
   opening the file.
4. `change_risk` on the symbol you are about to edit, before editing.

## Reading a response

Every tool returns the same envelope. Branch on `outcome`:

- `found` - use the items.
- `partial` - usable, but the item cap truncated the list.
- `ambiguous` - several symbols matched; retry with a node id or full path from a candidate.
- `empty` - nothing matched; the summary carries next-step hints.
- `unavailable` - no index is wired; run `graphi index`, then retry.

Confidence: `confirmed` and `derived` are safe to act on; `heuristic` should be
verified in the source first. Cite the `file:line` evidence in your answer.

## Falling back to grep

If graphi returns `empty` or `unavailable`, or you need a literal-text match,
grep is the right tool. The graphi-shunt hook blocks the first symbol-shaped
search it sees and allows the identical search a second time, so repeating the
exact command is the sanctioned fallback.

## Shell equivalents

The tools are plain CLI verbs with byte-identical output:

```bash
graphi search "<name>"
graphi definition "<symbol>"
graphi callers "<symbol>"
graphi references "<symbol>"
graphi impact "<symbol>"
graphi agent-brief -topic "<task>"
graphi related-files "<symbol>"
graphi explain-symbol "<symbol>"
git diff | graphi change-risk -diff -
```

## Keeping the graph honest

`graphi status` shows index health and `graphi sync` refreshes it after large
edits. If the source disagrees with the graph, trust the source and re-sync.
