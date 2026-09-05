package retrieval

// Exact binomial arithmetic for the SW-280 qrel-blind smoke evaluation.
//
// The gate's threshold k is derived here, by code, from the sealed dataset's
// population size N. It is never typed into a document and never adjusted after
// a response is opened. Two properties make that claim checkable rather than
// merely stated:
//
//  1. The pass/fail decision "does x/n clear the 0.75 floor?" is decided in
//     EXACT rational arithmetic, not floating point. P(X >= x | p) is strictly
//     increasing in p, so the Clopper-Pearson lower bound L(x,n) — the p at
//     which that upper tail equals alpha/2 — satisfies L(x,n) >= 3/4 exactly
//     when the upper tail evaluated at p = 3/4 is at most alpha/2. Both sides of
//     that comparison are rationals with exact big.Rat representations, so the
//     comparison has no rounding to argue about.
//  2. The published interval endpoints are produced by bisection on the same
//     exact tail, and every endpoint is reported together with the rational
//     bracket that encloses it. A reader can re-derive the digits.
//
// Nothing here reads a flag, an environment variable or a configuration key.

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	// QrelBlindSmokeFloorNumerator / QrelBlindSmokeFloorDenominator is the
	// 0.75 true-rate floor the decision record settled on (SW-266
	// decision-record.md section 4). It is written as a rational because the
	// gate compares rationals.
	QrelBlindSmokeFloorNumerator   = 3
	QrelBlindSmokeFloorDenominator = 4

	// QrelBlindSmokeLevelBasisPoints is the two-sided 95% confidence level.
	QrelBlindSmokeLevelBasisPoints = 9500

	// clopperPearsonBisectionRounds halves the [0,1] bracket this many times.
	// 2^-64 is far below any digit this package renders, and the bracket is
	// published so the reader never has to trust the last digit.
	clopperPearsonBisectionRounds = 64

	// clopperPearsonRenderedDecimals is how many decimal places an endpoint is
	// rendered to. It is a rendering width, not a precision claim: the exact
	// bracket travels beside every rendered value.
	clopperPearsonRenderedDecimals = 12

	// ClopperPearsonMethod names the derivation in machine artifacts.
	ClopperPearsonMethod = "two-sided exact Clopper-Pearson binomial interval; " +
		"the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, " +
		"and rendered endpoints are bisection brackets of width 2^-64"
)

// ErrNoPassingCountReachesBound is the typed sentinel AC-4 requires. It is
// returned when NO integer in [0, N] has a Clopper-Pearson lower bound at or
// above the floor, which is the case for every N <= 12 at floor 3/4. There is
// deliberately no clamp to N and no "best available" mode: an evaluation whose
// population cannot support the claim records RELEASE: NO instead.
var ErrNoPassingCountReachesBound = errors.New("retrieval qrel-blind smoke evaluation: no pass count in [0, N] reaches the confidence floor")

// UnsatisfiableBoundError names the population that cannot support the floor.
type UnsatisfiableBoundError struct {
	N                int
	FloorNumerator   int
	FloorDenominator int
	LevelBasisPoints int
}

func (e *UnsatisfiableBoundError) Error() string {
	return fmt.Sprintf("retrieval qrel-blind smoke evaluation: N=%d is too small for a %d/%d lower bound at %d basis points; "+
		"no integer in [0, %d] qualifies, k is not clamped to N and there is no best-available mode",
		e.N, e.FloorNumerator, e.FloorDenominator, e.LevelBasisPoints, e.N)
}

// Unwrap lets errors.Is(err, ErrNoPassingCountReachesBound) match.
func (e *UnsatisfiableBoundError) Unwrap() error { return ErrNoPassingCountReachesBound }

// alphaOverTwo is the one-sided tail mass at each end of a two-sided 95%
// interval: (10000 - 9500) / 2 basis points = 0.025 = 1/40.
func alphaOverTwo() *big.Rat {
	return big.NewRat(int64(10000-QrelBlindSmokeLevelBasisPoints), 2*10000)
}

func floorRat() *big.Rat {
	return big.NewRat(QrelBlindSmokeFloorNumerator, QrelBlindSmokeFloorDenominator)
}

// FloorString renders the floor as the exact rational it is.
func FloorString() string {
	return fmt.Sprintf("%d/%d", QrelBlindSmokeFloorNumerator, QrelBlindSmokeFloorDenominator)
}

// binomialUpperTail returns the exact P(X >= successes) for X ~ Binomial(trials, p).
// successes outside [0, trials] is a programming error and panics rather than
// silently answering, because every caller here derives it from a population count.
func binomialUpperTail(successes, trials int, p *big.Rat) *big.Rat {
	if trials < 0 || successes < 0 || successes > trials {
		panic(fmt.Sprintf("retrieval qrel-blind smoke evaluation: binomial tail successes=%d trials=%d out of range", successes, trials))
	}
	if successes == 0 {
		return big.NewRat(1, 1)
	}
	q := new(big.Rat).Sub(big.NewRat(1, 1), p)
	sum := new(big.Rat)
	for i := successes; i <= trials; i++ {
		term := new(big.Rat).SetInt(binomialCoefficient(trials, i))
		term.Mul(term, ratPow(p, i))
		term.Mul(term, ratPow(q, trials-i))
		sum.Add(sum, term)
	}
	return sum
}

// binomialLowerTail returns the exact P(X <= successes).
func binomialLowerTail(successes, trials int, p *big.Rat) *big.Rat {
	if successes >= trials {
		return big.NewRat(1, 1)
	}
	return new(big.Rat).Sub(big.NewRat(1, 1), binomialUpperTail(successes+1, trials, p))
}

func binomialCoefficient(n, k int) *big.Int {
	return new(big.Int).Binomial(int64(n), int64(k))
}

func ratPow(base *big.Rat, exp int) *big.Rat {
	out := big.NewRat(1, 1)
	if exp == 0 {
		return out
	}
	num := new(big.Int).Exp(base.Num(), big.NewInt(int64(exp)), nil)
	den := new(big.Int).Exp(base.Denom(), big.NewInt(int64(exp)), nil)
	return out.SetFrac(num, den)
}

// MeetsClopperPearsonFloor reports, exactly, whether the two-sided 95%
// Clopper-Pearson lower bound for successes/trials is at or above the 3/4 floor.
//
// It never bisects. The lower bound L solves P(X >= successes | L) = alpha/2, and
// that tail is strictly increasing in p, so L >= 3/4 exactly when the tail at
// p = 3/4 is at most alpha/2. Both quantities are exact rationals.
func MeetsClopperPearsonFloor(successes, trials int) (bool, error) {
	if trials < 0 {
		return false, fmt.Errorf("retrieval qrel-blind smoke evaluation: trials=%d is negative", trials)
	}
	if successes < 0 || successes > trials {
		return false, fmt.Errorf("retrieval qrel-blind smoke evaluation: successes=%d is outside [0, %d]", successes, trials)
	}
	if successes == 0 {
		// L(0, n) is exactly 0 and cannot clear a positive floor.
		return false, nil
	}
	tail := binomialUpperTail(successes, trials, floorRat())
	return tail.Cmp(alphaOverTwo()) <= 0, nil
}

// MinimumPassCount returns the smallest integer k in [0, trials] whose
// two-sided exact Clopper-Pearson 95% lower bound is at least 3/4.
//
// It is the ONLY producer of k in this package: there is no setter, no
// override argument and no floor parameter. A caller that wants a different
// floor has to change this file and its golden table, which is a reviewable
// event rather than a runtime one.
func MinimumPassCount(trials int) (int, error) {
	if trials < 0 {
		return 0, fmt.Errorf("retrieval qrel-blind smoke evaluation: trials=%d is negative", trials)
	}
	for x := 0; x <= trials; x++ {
		ok, err := MeetsClopperPearsonFloor(x, trials)
		if err != nil {
			return 0, err
		}
		if ok {
			return x, nil
		}
	}
	return 0, &UnsatisfiableBoundError{
		N:                trials,
		FloorNumerator:   QrelBlindSmokeFloorNumerator,
		FloorDenominator: QrelBlindSmokeFloorDenominator,
		LevelBasisPoints: QrelBlindSmokeLevelBasisPoints,
	}
}

// ExactBinomialInterval is one published Clopper-Pearson interval. The
// rendered endpoints are decimal strings; the brackets are the exact rational
// interval each endpoint provably lies inside, so a reader can check the digits
// instead of trusting them.
type ExactBinomialInterval struct {
	Successes        int    `json:"successes"`
	Trials           int    `json:"trials"`
	LevelBasisPoints int    `json:"level_basis_points"`
	Method           string `json:"method"`
	LowerBound       string `json:"lower_bound"`
	UpperBound       string `json:"upper_bound"`
	LowerBracketLow  string `json:"lower_bound_bracket_low"`
	LowerBracketHigh string `json:"lower_bound_bracket_high"`
	UpperBracketLow  string `json:"upper_bound_bracket_low"`
	UpperBracketHigh string `json:"upper_bound_bracket_high"`
	// MeetsFloor is decided by exact rational comparison, never from the
	// rendered decimals above.
	MeetsFloor bool   `json:"meets_floor"`
	Floor      string `json:"floor"`
}

// ClopperPearsonInterval computes the two-sided 95% exact interval for
// successes/trials, with brackets and the exact floor decision.
func ClopperPearsonInterval(successes, trials int) (ExactBinomialInterval, error) {
	if trials <= 0 {
		return ExactBinomialInterval{}, fmt.Errorf("retrieval qrel-blind smoke evaluation: trials=%d must be positive", trials)
	}
	if successes < 0 || successes > trials {
		return ExactBinomialInterval{}, fmt.Errorf("retrieval qrel-blind smoke evaluation: successes=%d is outside [0, %d]", successes, trials)
	}
	meets, err := MeetsClopperPearsonFloor(successes, trials)
	if err != nil {
		return ExactBinomialInterval{}, err
	}
	lowLo, lowHi := lowerBoundBracket(successes, trials)
	upLo, upHi := upperBoundBracket(successes, trials)
	return ExactBinomialInterval{
		Successes:        successes,
		Trials:           trials,
		LevelBasisPoints: QrelBlindSmokeLevelBasisPoints,
		Method:           ClopperPearsonMethod,
		LowerBound:       renderBracket(lowLo, lowHi),
		UpperBound:       renderBracket(upLo, upHi),
		LowerBracketLow:  renderRat(lowLo),
		LowerBracketHigh: renderRat(lowHi),
		UpperBracketLow:  renderRat(upLo),
		UpperBracketHigh: renderRat(upHi),
		MeetsFloor:       meets,
		Floor:            FloorString(),
	}, nil
}

// lowerBoundBracket brackets the p solving P(X >= successes | p) = alpha/2.
func lowerBoundBracket(successes, trials int) (*big.Rat, *big.Rat) {
	if successes == 0 {
		zero := new(big.Rat)
		return zero, new(big.Rat)
	}
	alpha := alphaOverTwo()
	lo, hi := new(big.Rat), big.NewRat(1, 1)
	for i := 0; i < clopperPearsonBisectionRounds; i++ {
		mid := new(big.Rat).Add(lo, hi)
		mid.Quo(mid, big.NewRat(2, 1))
		if binomialUpperTail(successes, trials, mid).Cmp(alpha) > 0 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return lo, hi
}

// upperBoundBracket brackets the p solving P(X <= successes | p) = alpha/2.
func upperBoundBracket(successes, trials int) (*big.Rat, *big.Rat) {
	if successes == trials {
		one := big.NewRat(1, 1)
		return one, big.NewRat(1, 1)
	}
	alpha := alphaOverTwo()
	lo, hi := new(big.Rat), big.NewRat(1, 1)
	for i := 0; i < clopperPearsonBisectionRounds; i++ {
		mid := new(big.Rat).Add(lo, hi)
		mid.Quo(mid, big.NewRat(2, 1))
		if binomialLowerTail(successes, trials, mid).Cmp(alpha) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, hi
}

// renderBracket renders the endpoint only when both ends of its bracket agree
// at the rendered width. Disagreement is a bug in the bisection budget, not
// something to paper over with a rounded average, so it renders the bracket.
func renderBracket(lo, hi *big.Rat) string {
	a, b := renderRat(lo), renderRat(hi)
	if a == b {
		return a
	}
	return "[" + a + ", " + b + "]"
}

// renderRat truncates toward zero at clopperPearsonRenderedDecimals places.
// Truncation (not rounding) keeps a rendered lower bound a true lower bound.
func renderRat(r *big.Rat) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(clopperPearsonRenderedDecimals), nil)
	scaled := new(big.Int).Mul(r.Num(), scale)
	scaled.Quo(scaled, r.Denom())
	digits := scaled.String()
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	for len(digits) <= clopperPearsonRenderedDecimals {
		digits = "0" + digits
	}
	cut := len(digits) - clopperPearsonRenderedDecimals
	out := digits[:cut] + "." + digits[cut:]
	if negative {
		out = "-" + out
	}
	return out
}
