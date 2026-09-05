package retrieval

import (
	"errors"
	"testing"
)

// AC-3: k is derived by code, with a golden table. The four values below were
// recomputed independently while the ticket was written, by bisection on the
// exact binomial tail P(X >= x) = alpha/2 with alpha = 0.05, and agree with
// SW-266's decision record. N=64 is today's sealed holdout population.
func TestQrelBlindSmoke_MinimumPassCountGoldenTable(t *testing.T) {
	for _, tc := range []struct {
		n             int
		wantK         int
		unsatisfiable bool
	}{
		{n: 8, unsatisfiable: true},
		{n: 12, unsatisfiable: true},
		{n: 13, wantK: 13},
		{n: 20, wantK: 19},
		{n: 30, wantK: 28},
		{n: 41, wantK: 37},
		{n: 64, wantK: 56},
	} {
		got, err := MinimumPassCount(tc.n)
		if tc.unsatisfiable {
			if err == nil {
				t.Errorf("MinimumPassCount(%d) = %d, want the typed unsatisfiable error", tc.n, got)
				continue
			}
			if !errors.Is(err, ErrNoPassingCountReachesBound) {
				t.Errorf("MinimumPassCount(%d) error %v does not match ErrNoPassingCountReachesBound", tc.n, err)
			}
			var typed *UnsatisfiableBoundError
			if !errors.As(err, &typed) {
				t.Errorf("MinimumPassCount(%d) error is not *UnsatisfiableBoundError", tc.n)
			} else if typed.N != tc.n {
				t.Errorf("MinimumPassCount(%d) error names N=%d", tc.n, typed.N)
			}
			// AC-4: never clamp to N.
			if got == tc.n && tc.n > 0 {
				t.Errorf("MinimumPassCount(%d) returned %d, which is N; k is never clamped", tc.n, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("MinimumPassCount(%d): %v", tc.n, err)
			continue
		}
		if got != tc.wantK {
			t.Errorf("MinimumPassCount(%d) = %d, want %d", tc.n, got, tc.wantK)
		}
	}
}

// The interval endpoints the umbrella quotes, plus the step below k in each
// golden case. A rendered lower bound is truncated toward zero, so a rendered
// value at or above the floor is a true lower bound at or above the floor.
func TestQrelBlindSmoke_ClopperPearsonEndpointsArePinned(t *testing.T) {
	for _, tc := range []struct {
		successes, trials int
		wantLower         string
		wantMeetsFloor    bool
	}{
		{27, 30, "0.734711549525", false},
		{28, 30, "0.779264598477", true},
		{18, 20, "0.683017285980", false},
		{19, 20, "0.751267237227", true},
		{12, 13, "0.639702564732", false},
		{13, 13, "0.752947361999", true},
		{12, 12, "0.735351530602", false},
		{55, 64, "0.749763164375", false},
		{56, 64, "0.768473694033", true},
		{64, 64, "0.943990910613", true},
	} {
		interval, err := ClopperPearsonInterval(tc.successes, tc.trials)
		if err != nil {
			t.Fatalf("ClopperPearsonInterval(%d,%d): %v", tc.successes, tc.trials, err)
		}
		if interval.LowerBound != tc.wantLower {
			t.Errorf("%d/%d lower bound = %s, want %s", tc.successes, tc.trials, interval.LowerBound, tc.wantLower)
		}
		if interval.MeetsFloor != tc.wantMeetsFloor {
			t.Errorf("%d/%d meets_floor = %t, want %t", tc.successes, tc.trials, interval.MeetsFloor, tc.wantMeetsFloor)
		}
		if interval.LevelBasisPoints != QrelBlindSmokeLevelBasisPoints {
			t.Errorf("%d/%d level = %d", tc.successes, tc.trials, interval.LevelBasisPoints)
		}
		if interval.Floor != "3/4" {
			t.Errorf("%d/%d floor = %q", tc.successes, tc.trials, interval.Floor)
		}
		// The published endpoint must lie inside the bracket it is derived
		// from, and the bracket must be tight enough that both ends render the
		// same digits.
		if interval.LowerBracketLow != interval.LowerBracketHigh {
			t.Errorf("%d/%d lower bracket [%s, %s] does not agree at the rendered width",
				tc.successes, tc.trials, interval.LowerBracketLow, interval.LowerBracketHigh)
		}
	}
}

// The exact floor decision and the rendered decimals must agree. This is the
// check that would catch a future refactor that decided the gate from the
// rendered string instead of the exact rational.
func TestQrelBlindSmoke_ExactFloorDecisionAgreesWithTheRenderedEndpoint(t *testing.T) {
	// The sweep is bounded because every pair costs 64 rounds of exact rational
	// arithmetic; the four populations this evaluation can actually meet (13,
	// 20, 30, 64) are checked explicitly beside it.
	populations := []int{}
	for n := 1; n <= 24; n++ {
		populations = append(populations, n)
	}
	populations = append(populations, 30, 41, 64)
	for _, n := range populations {
		for x := 0; x <= n; x++ {
			exact, err := MeetsClopperPearsonFloor(x, n)
			if err != nil {
				t.Fatalf("MeetsClopperPearsonFloor(%d,%d): %v", x, n, err)
			}
			interval, err := ClopperPearsonInterval(x, n)
			if err != nil {
				t.Fatalf("ClopperPearsonInterval(%d,%d): %v", x, n, err)
			}
			// The exact bound L lies inside [LowerBracketLow, LowerBracketHigh].
			// So L >= 3/4 implies the high end is at or above 0.75, and L < 3/4
			// implies the low end is below it. Both bracket strings have the
			// same fixed width, so a string comparison is a numeric one.
			const floor = "0.750000000000"
			if exact && interval.LowerBracketHigh < floor {
				t.Fatalf("%d/%d: exact decision says the bound clears 3/4 but its bracket high end is %s", x, n, interval.LowerBracketHigh)
			}
			if !exact && interval.LowerBracketLow >= floor {
				t.Fatalf("%d/%d: exact decision says the bound misses 3/4 but its bracket low end is %s", x, n, interval.LowerBracketLow)
			}
		}
	}
}

// AC-4: N=12 is pinned as unsatisfiable, and so is every smaller N, including
// the 8 answerable holdout queries the umbrella had before SW-279 sealed the
// v2 dataset. There is no best-available mode to fall back to.
func TestQrelBlindSmoke_EveryNBelowThirteenIsUnsatisfiable(t *testing.T) {
	for n := 0; n <= 12; n++ {
		k, err := MinimumPassCount(n)
		if err == nil {
			t.Errorf("MinimumPassCount(%d) = %d, want unsatisfiable", n, k)
			continue
		}
		if !errors.Is(err, ErrNoPassingCountReachesBound) {
			t.Errorf("MinimumPassCount(%d) error %v is not the typed sentinel", n, err)
		}
	}
	// And 13 is the first N that works, so the cliff is exactly where the
	// ticket says it is rather than merely somewhere below it.
	if k, err := MinimumPassCount(13); err != nil || k != 13 {
		t.Errorf("MinimumPassCount(13) = %d, %v; want 13, nil", k, err)
	}
}

// Refusal: a negative or out-of-range input is an error, not a silent zero.
func TestQrelBlindSmoke_OutOfRangeInputsAreRefused(t *testing.T) {
	if _, err := MinimumPassCount(-1); err == nil {
		t.Error("MinimumPassCount(-1) accepted a negative population")
	}
	if _, err := MeetsClopperPearsonFloor(5, 4); err == nil {
		t.Error("MeetsClopperPearsonFloor(5,4) accepted successes above trials")
	}
	if _, err := MeetsClopperPearsonFloor(-1, 4); err == nil {
		t.Error("MeetsClopperPearsonFloor(-1,4) accepted negative successes")
	}
	if _, err := ClopperPearsonInterval(1, 0); err == nil {
		t.Error("ClopperPearsonInterval(1,0) accepted an empty population")
	}
}
