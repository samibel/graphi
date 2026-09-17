# Finding: V4 public version was stale

The committed V4 capture is retained as negative development evidence and must
not be used for release. Although the internal selector stamped
`task_context/2-compact/4`, the actor-visible facade still overwrote that value
with `task_context/2-compact/3`. The V4 README also named the implementation
commit rather than the later frozen capture candidate.

The next candidate aliases the public version directly to the selector version
and is recaptured in a new run directory. V4 artifacts are not rewritten.
