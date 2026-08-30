package model2vec

import (
	"math"
	"testing"
)

// Known binary16 bit patterns (AC-2 test note: "a pure-Go float16→float32
// conversion test against known bit patterns").
func TestF16ToF32_KnownPatterns(t *testing.T) {
	cases := []struct {
		name string
		bits uint16
		want float32
	}{
		{"zero", 0x0000, 0},
		{"one", 0x3C00, 1},
		{"minus two", 0xC000, -2},
		{"max finite", 0x7BFF, 65504},
		{"smallest normal", 0x0400, float32(math.Ldexp(1, -14))},
		{"smallest subnormal", 0x0001, float32(math.Ldexp(1, -24))},
		{"largest subnormal", 0x03FF, float32(1023 * math.Ldexp(1, -24))},
		{"one third", 0x3555, 0.333251953125},
		{"pi-ish", 0x4248, 3.140625},
		{"negative subnormal", 0x8200, -float32(math.Ldexp(1, -15))}, // 512 × 2^-24
		{"oracle row0 c0", 0x15E1, 0.0014352798461914062},            // embedding_row_samples["0"][0]
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := f16ToF32(c.bits); got != c.want {
				t.Fatalf("f16ToF32(%#04x) = %v, want %v", c.bits, got, c.want)
			}
			if back := f32ToF16(c.want); back != c.bits {
				t.Fatalf("f32ToF16(%v) = %#04x, want %#04x", c.want, back, c.bits)
			}
		})
	}
	if v := f16ToF32(0x7C00); !math.IsInf(float64(v), 1) {
		t.Errorf("0x7C00 = %v, want +Inf", v)
	}
	if v := f16ToF32(0xFC00); !math.IsInf(float64(v), -1) {
		t.Errorf("0xFC00 = %v, want -Inf", v)
	}
	if v := f16ToF32(0x7E00); !math.IsNaN(float64(v)) {
		t.Errorf("0x7E00 = %v, want NaN", v)
	}
	if v := f16ToF32(0x8000); v != 0 || math.Signbit(float64(v)) == false {
		t.Errorf("0x8000 = %v, want -0", v)
	}
}

// Every binary16 value survives a round trip through float32 exactly.
func TestF16_RoundTripAllPatterns(t *testing.T) {
	for i := 0; i < 1<<16; i++ {
		h := uint16(i)
		if h&0x7C00 == 0x7C00 && h&0x3FF != 0 {
			continue // NaN payloads are not required to round-trip bit-exactly
		}
		if back := f32ToF16(f16ToF32(h)); back != h {
			t.Fatalf("round trip %#04x → %v → %#04x", h, f16ToF32(h), back)
		}
	}
}

// f32ToF16 rounds to nearest, ties to even — the numpy conversion rule.
func TestF32ToF16_RoundToNearestEven(t *testing.T) {
	ulp := float32(math.Ldexp(1, -10)) // binary16 ulp at 1.0
	cases := []struct {
		name string
		in   float32
		want uint16
	}{
		{"tie below odd rounds up to even", 1 + 1.5*ulp, 0x3C02},
		{"tie above even rounds down to even", 1 + 0.5*ulp, 0x3C00},
		{"just above tie rounds up", 1 + 0.5*ulp + float32(math.Ldexp(1, -20)), 0x3C01},
		{"just below tie rounds down", 1 + 0.5*ulp - float32(math.Ldexp(1, -20)), 0x3C00},
		{"overflow to inf", 70000, 0x7C00},
		{"65520 is a tie with odd mantissa: inf", 65520, 0x7C00},
		{"65519 rounds down to max", 65519, 0x7BFF},
		{"below half smallest subnormal", float32(math.Ldexp(1, -26)), 0x0000},
		{"exactly half smallest subnormal ties to zero", float32(math.Ldexp(1, -25)), 0x0000},
		{"above half smallest subnormal", float32(math.Ldexp(1, -25)) * 1.5, 0x0001},
		{"subnormal tie rounds to even", float32(math.Ldexp(1, -24)) * 2.5, 0x0002},
		{"subnormal tie rounds to even (odd neighbour)", float32(math.Ldexp(1, -24)) * 3.5, 0x0004},
		{"float32 subnormal is zero", float32(math.Ldexp(1, -130)), 0x0000},
		{"negative zero", float32(math.Copysign(0, -1)), 0x8000},
		{"largest subnormal carries into normal", float32(math.Ldexp(1, -14)) - float32(math.Ldexp(1, -26)), 0x0400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := f32ToF16(c.in); got != c.want {
				t.Fatalf("f32ToF16(%v) = %#04x, want %#04x", c.in, got, c.want)
			}
		})
	}
	if got := f32ToF16(float32(math.NaN())); got&0x7C00 != 0x7C00 || got&0x3FF == 0 {
		t.Errorf("NaN → %#04x is not a NaN", got)
	}
	if got := f32ToF16(float32(math.Inf(-1))); got != 0xFC00 {
		t.Errorf("-Inf → %#04x", got)
	}
}

// The pairwise sum reproduces numpy's tree: for fewer than 8 elements it is the
// plain sequential sum; for 8..128 it is the 8-lane interleave; above 128 it
// splits at a multiple of 8. The tree is pinned by a value that depends on
// the association.
func TestPairwiseSumF16_Tree(t *testing.T) {
	seq := func(a []uint16) float32 {
		var s float32
		for _, h := range a {
			s += f16ToF32(h)
		}
		return s
	}
	small := []uint16{0x3C00, 0x3555, 0x4248, 0x0001, 0xC000}
	if got, want := pairwiseSumF16(small), seq(small); got != want {
		t.Fatalf("n<8: pairwise %v != sequential %v", got, want)
	}
	// 16 elements: lanes r[j] = a[j] + a[j+8], combined as the fixed tree.
	a := make([]uint16, 16)
	for i := range a {
		a[i] = f32ToF16(float32(i+1) / 3)
	}
	var r [8]float32
	for j := 0; j < 8; j++ {
		r[j] = f16ToF32(a[j]) + f16ToF32(a[j+8])
	}
	want := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
	if got := pairwiseSumF16(a); got != want {
		t.Fatalf("n=16: pairwise %v != hand-built tree %v", got, want)
	}
	// 256 elements: two 128-halves; the value is deterministic and within
	// float32 noise of the sequential sum.
	b := make([]uint16, 256)
	for i := range b {
		b[i] = f32ToF16(float32(i%17) * 0.013)
	}
	first := pairwiseSumF16(b)
	if second := pairwiseSumF16(b); second != first {
		t.Fatalf("pairwise sum is not deterministic: %v vs %v", first, second)
	}
	if want := pairwiseSumF16(b[:128]) + pairwiseSumF16(b[128:]); first != want {
		t.Fatalf("n=256 does not split into two halves of 128: %v vs %v", first, want)
	}
	if diff := math.Abs(float64(first - seq(b))); diff > 1e-3 {
		t.Fatalf("pairwise %v is far from sequential %v", first, seq(b))
	}
}
