# Finding: V5 did not authorize the holdout

The V5 product payload evidence is internally valid for frozen candidate
`4f9a39f809859e9f15e162c30da28900fc946bf4`: its public versions, byte
identity, token counts, source overlap and candidate binding all verify.

It does not authorize the fresh holdout. Independent audit found that the seal
phase named the run-local grading-rubric path but did not re-read and hash-check
those bytes before generating irreversible grader packets. A drifted rubric
would only have been rejected later by `decide`.

The successor candidate verifies the rubric against the SHA frozen in the
precondition record before producing any packet and embeds the exact verified
rubric bytes and digest in every packet. V5 artifacts are retained unchanged as
negative development evidence; the successor is captured in a new directory.
