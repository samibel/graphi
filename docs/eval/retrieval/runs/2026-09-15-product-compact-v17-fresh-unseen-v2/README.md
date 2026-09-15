# Fresh unseen compact-17 holdout

Status: **curator-sealed and not captured**.

This directory preregisters a new 64-query Cobra holdout for product source
commit `26a36f0958cb16d925c4dd871cda76d0e117b15a` and source tree
`6ab1d7075c787b7a8884114d5d732be812cb325f`. It uses the unchanged 1,200-token
budget, qrel-blind contract 2, and decision threshold `k=56/64`.

The sealed dataset is confidential. Do not print, manually inspect, copy, or
send it through the root orchestration channel. The public files in this
directory disclose only method, aggregate counts, digests, independence, and
operator sequencing.

No capture, primary response, grade, adjudication, or release result exists at
curator handoff. Follow `OPERATOR-HANDOFF.md` exactly. In particular, every
`seal` invocation must receive `-checkout` pointing to the clean pinned Cobra
checkout, and no rater may run before the complete capture commit exists.
