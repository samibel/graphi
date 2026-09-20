package retrieval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"
)

const (
	// SavingsAggregateVersion identifies the result shape and calculation below.
	// It does not change the frozen measurement contract; it implements that
	// contract's previously missing aggregation step.
	SavingsAggregateVersion = "sw266-savings-aggregate/1"

	// SavingsBootstrapSamplerVersion pins a CGo-free random stream and bounded
	// sampler independently of Go's math/rand implementation. The stream emits
	// the four big-endian uint64s from
	//
	//   SHA256(seed_digest || uint64_big_endian(counter))
	//
	// for counters starting at zero. A bounded draw rejects x below
	// (-bound mod bound), then returns x mod bound. Replicates are traversed in
	// order, dev before holdout, drawing the sorted family count of each split.
	SavingsBootstrapSamplerVersion = "sha256-counter-be64-modulo-threshold-rejection/1"

	SavingsReplicateDigestEncoding  = "canonical-json exact rationals in replicate order"
	SavingsAggregateRendererVersion = "signed-half-away-from-zero/1"
)

// SavingsExactRational is a normalized exact fraction. Denominator is always
// positive; numerator and denominator are base-10 integers with no leading
// zeroes. It is the only numeric representation used before display rendering.
type SavingsExactRational struct {
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

// SavingsQueryValue preserves both integer inputs to 100*(G-C)/G as well as
// the resulting exact fraction. It also carries the clustering coordinates
// needed to reproduce every bootstrap replicate.
type SavingsQueryValue struct {
	QueryID         string               `json:"query_id"`
	FamilyID        string               `json:"family_id"`
	Split           string               `json:"split"`
	CandidateTokens int                  `json:"candidate_tokens"`
	GrepReadTokens  int                  `json:"grepread_tokens"`
	PercentSavings  SavingsExactRational `json:"percent_savings"`
}

// SavingsSplitAggregate records the frozen family sampling frame. FamilyIDs
// are sorted bytewise and are therefore also the bounded-sampler index order.
type SavingsSplitAggregate struct {
	Split       string   `json:"split"`
	N           int      `json:"n"`
	FamilyCount int      `json:"family_count"`
	FamilyIDs   []string `json:"family_ids"`
}

// SavingsBootstrapAggregate contains the complete unrounded bootstrap output.
// Together with QueryValues, SplitAggregates, SeedSHA256 and the pinned sampler
// identity, ReplicateMedians is independently reproducible. The two digests
// make stream and arithmetic drift easy to localize.
type SavingsBootstrapAggregate struct {
	SamplerVersion          string                 `json:"sampler_version"`
	SeedMethod              string                 `json:"seed_method"`
	SeedSHA256              string                 `json:"seed_sha256"`
	Replicates              int                    `json:"replicates"`
	DrawCount               int                    `json:"draw_count"`
	DrawsSHA256             string                 `json:"draws_sha256"`
	ReplicateDigestEncoding string                 `json:"replicate_digest_encoding"`
	ReplicateMediansSHA256  string                 `json:"replicate_medians_sha256"`
	ReplicateMedians        []SavingsExactRational `json:"replicate_medians"`
	LowerNearestRank        int                    `json:"lower_nearest_rank"`
	UpperNearestRank        int                    `json:"upper_nearest_rank"`
	Lower                   SavingsExactRational   `json:"lower"`
	Upper                   SavingsExactRational   `json:"upper"`
}

// SavingsAggregateResult is a self-contained, content-addressed result. Input
// is retained intentionally: validation can recompute payload token counts,
// per-query fractions, sampling, all replicates and both interval endpoints
// without trusting a second unbound artifact.
type SavingsAggregateResult struct {
	Version     string                    `json:"version"`
	InputSHA256 string                    `json:"input_sha256"`
	Input       SavingsAggregateInput     `json:"input"`
	N           int                       `json:"n"`
	FamilyCount int                       `json:"family_count"`
	Splits      []SavingsSplitAggregate   `json:"splits"`
	QueryValues []SavingsQueryValue       `json:"query_values"`
	Median      SavingsExactRational      `json:"median"`
	Bootstrap   SavingsBootstrapAggregate `json:"bootstrap"`
	SHA256      string                    `json:"sha256"`
}

// SavingsAggregateDisplay is the only rounded view of an aggregate. Exact
// fractions in SavingsAggregateResult remain authoritative for every check.
type SavingsAggregateDisplay struct {
	RendererVersion string `json:"renderer_version"`
	N               int    `json:"n"`
	FamilyCount     int    `json:"family_count"`
	MedianPercent   string `json:"median_percent"`
	LowerPercent    string `json:"lower_percent"`
	UpperPercent    string `json:"upper_percent"`
}

// ComputeSavingsAggregate validates the complete equal-recall population
// before calculating any magnitude. A miss or incomplete population therefore
// cannot be converted into a median or finite penalty by this module.
func ComputeSavingsAggregate(in SavingsAggregateInput, counters map[string]PayloadCounter) (SavingsAggregateResult, error) {
	if err := ValidateSavingsAggregateInput(in, counters); err != nil {
		return SavingsAggregateResult{}, fmt.Errorf("retrieval savings aggregate: validate input: %w", err)
	}
	return computeValidatedSavingsAggregate(in)
}

func computeValidatedSavingsAggregate(in SavingsAggregateInput) (SavingsAggregateResult, error) {
	inputBytes, err := json.Marshal(in)
	if err != nil {
		return SavingsAggregateResult{}, fmt.Errorf("retrieval savings aggregate: marshal input: %w", err)
	}
	// Detach the content-addressed record from caller-owned slice backing arrays.
	// The same canonical bytes define InputSHA256, so this copy cannot introduce
	// a representation not covered by the address.
	var frozenInput SavingsAggregateInput
	if err := json.Unmarshal(inputBytes, &frozenInput); err != nil {
		return SavingsAggregateResult{}, fmt.Errorf("retrieval savings aggregate: freeze input: %w", err)
	}
	in = frozenInput

	values := make([]SavingsQueryValue, len(in.Population))
	rationalValues := make([]*big.Rat, len(in.Population))
	families := map[string]map[string][]*big.Rat{
		SplitDev:     {},
		SplitHoldout: {},
	}
	for i, member := range in.Population {
		observation := in.Observations[i]
		candidateTokens := *observation.Candidate.TokensToTarget
		grepreadTokens := *observation.GrepRead.TokensToTarget
		if candidateTokens < 0 {
			return SavingsAggregateResult{}, fmt.Errorf("retrieval savings aggregate: query %s candidate tokens-to-target must be non-negative", member.QueryID)
		}
		ratio := percentSavings(candidateTokens, grepreadTokens)
		exact := exactRational(ratio)
		values[i] = SavingsQueryValue{
			QueryID:         member.QueryID,
			FamilyID:        member.FamilyID,
			Split:           member.Split,
			CandidateTokens: candidateTokens,
			GrepReadTokens:  grepreadTokens,
			PercentSavings:  exact,
		}
		rationalValues[i] = ratio
		families[member.Split][member.FamilyID] = append(families[member.Split][member.FamilyID], ratio)
	}

	splits := make([]SavingsSplitAggregate, 0, 2)
	totalFamilies := 0
	for _, split := range []string{SplitDev, SplitHoldout} {
		familyIDs := make([]string, 0, len(families[split]))
		queryCount := 0
		for familyID, familyValues := range families[split] {
			familyIDs = append(familyIDs, familyID)
			queryCount += len(familyValues)
		}
		sort.Strings(familyIDs)
		splits = append(splits, SavingsSplitAggregate{
			Split: split, N: queryCount, FamilyCount: len(familyIDs), FamilyIDs: familyIDs,
		})
		totalFamilies += len(familyIDs)
	}

	seed := sha256.Sum256([]byte(in.DatasetSHA256 + "\n" + in.Contract.Version))
	bootstrap, err := computeSavingsBootstrap(seed, families, splits)
	if err != nil {
		return SavingsAggregateResult{}, err
	}
	result := SavingsAggregateResult{
		Version:     SavingsAggregateVersion,
		InputSHA256: SHA256Hex(inputBytes),
		Input:       in,
		N:           len(values),
		FamilyCount: totalFamilies,
		Splits:      splits,
		QueryValues: values,
		Median:      exactRational(savingsMedian(rationalValues)),
		Bootstrap:   bootstrap,
	}
	address, err := ContentAddress(result, func(v *SavingsAggregateResult) { v.SHA256 = "" })
	if err != nil {
		return SavingsAggregateResult{}, fmt.Errorf("retrieval savings aggregate: address result: %w", err)
	}
	result.SHA256 = address
	return result, nil
}

// ValidateSavingsAggregateResult recomputes a result from its embedded input
// after executing the frozen input validator. It compares the full canonical
// structure, including all 10,000 exact replicate medians and the content
// address; a self-consistent edit to a derived field is still rejected.
func ValidateSavingsAggregateResult(got SavingsAggregateResult, counters map[string]PayloadCounter) error {
	want, err := ComputeSavingsAggregate(got.Input, counters)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("retrieval savings aggregate: result does not recompute exactly from its embedded validated input")
	}
	return nil
}

// RenderSavingsAggregate validates before converting the three authoritative
// exact fractions to one-decimal percentages. Rounding is signed half away
// from zero and occurs nowhere in the calculator or result artifact.
func RenderSavingsAggregate(result SavingsAggregateResult, counters map[string]PayloadCounter) (SavingsAggregateDisplay, error) {
	if err := ValidateSavingsAggregateResult(result, counters); err != nil {
		return SavingsAggregateDisplay{}, err
	}
	median, err := renderSavingsPercent(result.Median, result.Input.Contract.DisplayDecimalPlaces)
	if err != nil {
		return SavingsAggregateDisplay{}, err
	}
	lower, err := renderSavingsPercent(result.Bootstrap.Lower, result.Input.Contract.DisplayDecimalPlaces)
	if err != nil {
		return SavingsAggregateDisplay{}, err
	}
	upper, err := renderSavingsPercent(result.Bootstrap.Upper, result.Input.Contract.DisplayDecimalPlaces)
	if err != nil {
		return SavingsAggregateDisplay{}, err
	}
	return SavingsAggregateDisplay{
		RendererVersion: SavingsAggregateRendererVersion,
		N:               result.N,
		FamilyCount:     result.FamilyCount,
		MedianPercent:   median,
		LowerPercent:    lower,
		UpperPercent:    upper,
	}, nil
}

func computeSavingsBootstrap(seed [sha256.Size]byte, families map[string]map[string][]*big.Rat, splits []SavingsSplitAggregate) (SavingsBootstrapAggregate, error) {
	sampler := newSavingsHashCounterSampler(seed)
	drawHasher := sha256.New()
	replicates := make([]SavingsExactRational, SavingsBootstrapReplicates)
	replicateRationals := make([]*big.Rat, SavingsBootstrapReplicates)
	drawCount := 0
	var encodedIndex [8]byte
	for replicate := 0; replicate < SavingsBootstrapReplicates; replicate++ {
		resampled := make([]*big.Rat, 0)
		for _, split := range splits {
			for draw := 0; draw < split.FamilyCount; draw++ {
				index := sampler.Uint64n(uint64(split.FamilyCount))
				binary.BigEndian.PutUint64(encodedIndex[:], index)
				_, _ = drawHasher.Write(encodedIndex[:])
				drawCount++
				familyID := split.FamilyIDs[int(index)]
				resampled = append(resampled, families[split.Split][familyID]...)
			}
		}
		if len(resampled) == 0 {
			return SavingsBootstrapAggregate{}, fmt.Errorf("retrieval savings aggregate: bootstrap replicate %d sampled an empty population", replicate)
		}
		median := savingsMedian(resampled)
		replicateRationals[replicate] = median
		replicates[replicate] = exactRational(median)
	}

	encodedReplicates, err := json.Marshal(replicates)
	if err != nil {
		return SavingsBootstrapAggregate{}, fmt.Errorf("retrieval savings aggregate: marshal replicate medians: %w", err)
	}
	ordered := append([]*big.Rat(nil), replicateRationals...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Cmp(ordered[j]) < 0 })
	tailBasisPoints := (10000 - SavingsConfidenceLevelBasisPoints) / 2
	lowerRank := nearestRank(SavingsBootstrapReplicates, tailBasisPoints, 10000)
	upperRank := nearestRank(SavingsBootstrapReplicates, 10000-tailBasisPoints, 10000)
	return SavingsBootstrapAggregate{
		SamplerVersion:          SavingsBootstrapSamplerVersion,
		SeedMethod:              SavingsBootstrapSeedMethod,
		SeedSHA256:              hex.EncodeToString(seed[:]),
		Replicates:              SavingsBootstrapReplicates,
		DrawCount:               drawCount,
		DrawsSHA256:             hex.EncodeToString(drawHasher.Sum(nil)),
		ReplicateDigestEncoding: SavingsReplicateDigestEncoding,
		ReplicateMediansSHA256:  SHA256Hex(encodedReplicates),
		ReplicateMedians:        replicates,
		LowerNearestRank:        lowerRank,
		UpperNearestRank:        upperRank,
		Lower:                   exactRational(ordered[lowerRank-1]),
		Upper:                   exactRational(ordered[upperRank-1]),
	}, nil
}

func percentSavings(candidateTokens, grepreadTokens int) *big.Rat {
	g := new(big.Int)
	g.SetString(strconv.Itoa(grepreadTokens), 10)
	c := new(big.Int)
	c.SetString(strconv.Itoa(candidateTokens), 10)
	numerator := new(big.Int).Sub(new(big.Int).Set(g), c)
	numerator.Mul(numerator, big.NewInt(100))
	return new(big.Rat).SetFrac(numerator, g)
}

func savingsMedian(values []*big.Rat) *big.Rat {
	ordered := append([]*big.Rat(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Cmp(ordered[j]) < 0 })
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return new(big.Rat).Set(ordered[middle])
	}
	sum := new(big.Rat).Add(ordered[middle-1], ordered[middle])
	return sum.Quo(sum, big.NewRat(2, 1))
}

func exactRational(value *big.Rat) SavingsExactRational {
	return SavingsExactRational{Numerator: value.Num().String(), Denominator: value.Denom().String()}
}

func parseExactRational(value SavingsExactRational) (*big.Rat, error) {
	numerator, ok := new(big.Int).SetString(value.Numerator, 10)
	if !ok {
		return nil, fmt.Errorf("invalid exact rational numerator %q", value.Numerator)
	}
	denominator, ok := new(big.Int).SetString(value.Denominator, 10)
	if !ok || denominator.Sign() <= 0 {
		return nil, fmt.Errorf("invalid exact rational denominator %q", value.Denominator)
	}
	parsed := new(big.Rat).SetFrac(numerator, denominator)
	if exactRational(parsed) != value {
		return nil, fmt.Errorf("exact rational %s/%s is not normalized", value.Numerator, value.Denominator)
	}
	return parsed, nil
}

func nearestRank(n, numerator, denominator int) int {
	return (n*numerator + denominator - 1) / denominator
}

func renderSavingsPercent(value SavingsExactRational, decimalPlaces int) (string, error) {
	ratio, err := parseExactRational(value)
	if err != nil {
		return "", fmt.Errorf("retrieval savings aggregate: render: %w", err)
	}
	if decimalPlaces < 0 {
		return "", fmt.Errorf("retrieval savings aggregate: render: decimal places must be non-negative")
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalPlaces)), nil)
	absNumerator := new(big.Int).Abs(new(big.Int).Set(ratio.Num()))
	scaled := new(big.Int).Mul(absNumerator, scale)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled, ratio.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(ratio.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	negative := ratio.Sign() < 0 && quotient.Sign() != 0
	digits := quotient.String()
	if decimalPlaces > 0 {
		for len(digits) <= decimalPlaces {
			digits = "0" + digits
		}
		cut := len(digits) - decimalPlaces
		digits = digits[:cut] + "." + digits[cut:]
	}
	if negative {
		digits = "-" + digits
	}
	return digits + "%", nil
}

type savingsHashCounterSampler struct {
	seed    [sha256.Size]byte
	counter uint64
	block   [sha256.Size]byte
	offset  int
}

func newSavingsHashCounterSampler(seed [sha256.Size]byte) *savingsHashCounterSampler {
	return &savingsHashCounterSampler{seed: seed, offset: sha256.Size}
}

func (s *savingsHashCounterSampler) Uint64n(bound uint64) uint64 {
	if bound == 0 {
		panic("retrieval savings aggregate: zero sampler bound")
	}
	threshold := (uint64(0) - bound) % bound
	for {
		value := s.uint64()
		if value >= threshold {
			return value % bound
		}
	}
}

func (s *savingsHashCounterSampler) uint64() uint64 {
	if s.offset == sha256.Size {
		var material [sha256.Size + 8]byte
		copy(material[:sha256.Size], s.seed[:])
		binary.BigEndian.PutUint64(material[sha256.Size:], s.counter)
		s.block = sha256.Sum256(material[:])
		s.counter++
		s.offset = 0
	}
	value := binary.BigEndian.Uint64(s.block[s.offset : s.offset+8])
	s.offset += 8
	return value
}
