// Package review is graphi's PR-review PUBLISHER layer (EP-007 story 4/5,
// SW-042). It is the first inhabitant of the engine/review layer named in the
// project architecture context.
//
// Unlike the read-only engine/analysis siblings (SW-039 pr-risk, SW-040
// pr-signals, SW-041 pr-questions), this package PUBLISHES: it renders the three
// sibling REPORTS into one sticky Markdown PR comment and, optionally, evaluates
// a risk-threshold merge gate. It consumes the sibling reports through a single
// injectable findingsSource seam (consume, never recompute) — exactly as
// pr-questions consumes pr-risk + pr-signals via its questionSource seam.
//
// The single outbound boundary
//
// graphi is a local-first, CI-enforced ZERO-OUTBOUND engine. The PR-host comment
// API is the ONLY outbound network this engine performs, and it is isolated
// behind one small, total, mockable interface: CommentHost (host.go). The
// renderer (render.go) and the merge gate (gate.go) import NO network or exec
// packages and run/test fully offline; a MockHost is the test double in every
// test. The real network implementation lives behind the CommentHost seam and is
// the only place a host token is held — full real-host wiring + GitHub Action
// packaging is SW-043's scope.
//
// Determinism is a contract property
//
// The rendered body and the serialized PublishResult are byte-stable for
// byte-identical input (versioned schema + hashed config + canonical ordering +
// materialized never-null slices + SetEscapeHTML(false) + trailing-newline
// trim), mirroring analysis.MarshalRisk / MarshalSignals / MarshalQuestions, so
// SW-043 can pin a known contract.
package review
