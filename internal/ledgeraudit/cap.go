package ledgeraudit

// Policy is the anti-gaming cap policy. The cap formula is part of the pinned
// baseline: a reviewer blesses an intentional change by bumping Policy.Version
// alongside the BaselineMethodVersion, not by silently editing the clamps.
type Policy struct {
	// CapPerOpMicroUSD clamps the maximum savings a single operation may record.
	CapPerOpMicroUSD MicroUSD
	// CapPerSessionMicroUSD clamps the maximum cumulative savings a single
	// session may contribute, preventing inflation via repeated operations.
	CapPerSessionMicroUSD MicroUSD
	// Version stamps the cap formula (part of the pinned baseline).
	Version string
}

// DefaultPolicy returns the frozen, defensible default cap policy. The bounds
// are conservative: a single op can claim at most 10,000 micro-USD ($0.01), and
// a single session at most 1,000,000 micro-USD ($1.00). These are the defensible
// bounds the anti-gaming tests assert no entry/session exceeds.
func DefaultPolicy() Policy {
	return Policy{
		CapPerOpMicroUSD:      10_000,
		CapPerSessionMicroUSD: 1_000_000,
		Version:               BaselineMethodVersion,
	}
}

// CapPerOp clamps a raw per-operation savings to the policy bound.
func CapPerOp(raw MicroUSD, p Policy) MicroUSD {
	if raw > p.CapPerOpMicroUSD {
		return p.CapPerOpMicroUSD
	}
	if raw < 0 {
		return 0
	}
	return raw
}

// CapSession clamps a raw per-session cumulative savings to the policy bound.
func CapSession(raw MicroUSD, p Policy) MicroUSD {
	if raw > p.CapPerSessionMicroUSD {
		return p.CapPerSessionMicroUSD
	}
	if raw < 0 {
		return 0
	}
	return raw
}
