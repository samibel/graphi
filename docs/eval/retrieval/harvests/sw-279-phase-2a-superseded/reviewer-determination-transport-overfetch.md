# Reviewer determination: transport-level overfetch

**Status: reviewer's reading, recorded so the owner can overturn it. Not the selector's
self-certification.**

## What was disclosed

Phase 2a disclosed, unprompted, that `gh auth status` reported an invalid token in its sandbox and
direct network access was blocked, so the installed GitHub connector was used instead. That
connector transported broader normalized envelopes containing label and comment metadata. Those
values were discarded and, per the selector, never exposed to selection. The selector explicitly
declined to certify compliance, writing: *"If Section 1 forbids transport-level overfetch itself,
the run requires owner review and cannot be certified compliant without reinterpretation."*

Declining to self-certify an ambiguity is the correct behaviour and is the reason this
determination exists rather than a silent pass.

## The ambiguity

Section 1 says labels, reactions, comments, maintainer replies, linked pull requests, closing
events and external links *"must not be fetched or read for selection."* That parses two ways:

- **(a)** must not be *fetched*, or *read for selection* — the fetch itself is forbidden;
- **(b)** must not be fetched-for-selection or read-for-selection — the prohibition is on use.

## The determination, and why

**Reading (b).** The rule's own detectability clause settles it. In the same paragraph it
enumerates what a reviewer can detect, and the relevant item is *"selection evidence from a comment
or linked resource"* — evidence **used**, not bytes **present in a response envelope**. A control
is what it can detect; this rule says so about itself throughout, and section 8 is explicit that
undisclosed viewing is made a falsification rather than an uncheckable permission.

Under reading (a) the clause would also be undetectable from the artifacts: nothing in the ledger
distinguishes a title fetched alone from a title fetched inside a larger envelope. A rule clause
that cannot be checked is a statement of intent, and this rule deliberately does not contain those.

**So the run is compliant as written**, and the overfetch is a transport property, not selection
evidence.

## What changes if the owner reads it the other way

The harvest would have to be re-run with a transport that retrieves only title, opening body,
issue number, author and creation time. Nothing else in the phase is affected: the population
manifest, the classification clauses and the derived queries are unchanged by how the bytes
arrived, and all 66 derived queries were independently re-derived by the reviewer from the raw
titles and matched byte-for-byte.

## Recorded for the next reader

The wording *"must not be fetched or read for selection"* should be tightened in any future
revision of the rule — not because this run was wrong, but because the next reader should not have
to re-derive this determination. Tightening it now is out of the question: amending a frozen rule
after seeing the candidates is precisely what phase 1 was separated to prevent.
