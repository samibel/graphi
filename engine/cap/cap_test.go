package cap

import "testing"

// AC: per-op cap — contribution above PerOp clamps to PerOp; at/below unchanged.
func TestApply_PerOp(t *testing.T) {
	c := Cap{PerOpMicroUSD: 1_000_000}
	// Above -> clamped.
	if got, capped := c.Apply(2_500_000, 0); got != 1_000_000 || !capped {
		t.Errorf("over PerOp: want 1000000/capped, got %d/%v", got, capped)
	}
	// At bound -> unchanged, not capped.
	if got, capped := c.Apply(1_000_000, 0); got != 1_000_000 || capped {
		t.Errorf("at PerOp: want 1000000/not-capped, got %d/%v", got, capped)
	}
	// Below -> unchanged.
	if got, capped := c.Apply(100_000, 0); got != 100_000 || capped {
		t.Errorf("below PerOp: want 100000/not-capped, got %d/%v", got, capped)
	}
}

// AC: per-session cap — session total never exceeds PerSession.
func TestApply_PerSession(t *testing.T) {
	c := Cap{PerSessionMicroUSD: 1_000_000}
	// Session already at 800k; a 500k contribution would reach 1.3M -> clamp to 200k (room).
	if got, capped := c.Apply(500_000, 800_000); got != 200_000 || !capped {
		t.Errorf("session over ceiling: want 200000/capped, got %d/%v", got, capped)
	}
	// Session at ceiling already -> room 0 -> contribution clamped to 0.
	if got, capped := c.Apply(500_000, 1_000_000); got != 0 || !capped {
		t.Errorf("session at ceiling: want 0/capped, got %d/%v", got, capped)
	}
	// Small contribution that fits -> unchanged.
	if got, capped := c.Apply(100_000, 0); got != 100_000 || capped {
		t.Errorf("fits in session: want 100000/not-capped, got %d/%v", got, capped)
	}
}

// AC: 0 = unlimited (zero-value Cap passes everything through).
func TestApply_Unlimited(t *testing.T) {
	c := Cap{}
	if got, capped := c.Apply(9_999_999_999, 9_999_999_999); got != 9_999_999_999 || capped {
		t.Errorf("unlimited cap should pass through, got %d/%v", got, capped)
	}
}

// AC: per-op AND per-session combined — tighter bound wins.
func TestApply_BothBoundsTighterWins(t *testing.T) {
	// PerOp=1M, PerSession=1.5M, session at 1.2M. Contribution 5M:
	// per-op clamps to 1M; per-session room = 300k -> clamps further to 300k.
	c := Cap{PerOpMicroUSD: 1_000_000, PerSessionMicroUSD: 1_500_000}
	if got, capped := c.Apply(5_000_000, 1_200_000); got != 300_000 || !capped {
		t.Errorf("combined bounds: want 300000/capped, got %d/%v", got, capped)
	}
}

// AC: negative contributions (honest overruns) pass through untouched.
func TestApply_NegativeUnclamped(t *testing.T) {
	c := Cap{PerOpMicroUSD: 1_000_000, PerSessionMicroUSD: 1_000_000}
	if got, capped := c.Apply(-500_000, 0); got != -500_000 || capped {
		t.Errorf("negative contribution must pass through, got %d/%v", got, capped)
	}
	// Negative contribution to a near-ceiling session still passes (reduces total).
	if got, capped := c.Apply(-100_000, 1_000_000); got != -100_000 || capped {
		t.Errorf("negative near ceiling must pass through, got %d/%v", got, capped)
	}
}
