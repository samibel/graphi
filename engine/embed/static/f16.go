// Binary16 (IEEE 754 half precision) codec for the static embedder. The
// conversion is exact: every binary16 value is exactly representable in
// binary32, so f16ToF32 never loses information. The f32ToF16 conversion
// uses round-to-nearest, ties-to-even — the same rule the numpy reference
// uses for "store into a float16 array".
package static

import "math"

// f16ToF32 decodes an IEEE 754 binary16 bit pattern into a float32. Every
// binary16 value is exactly representable in binary32, so the conversion is
// exact: subnormals are renormalised, infinities and NaNs are propagated.
func f16ToF32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	switch exp {
	case 0:
		if mant == 0 {
			return math.Float32frombits(sign) // ±0
		}
		// Subnormal: value = mant × 2^-24. Shift the leading one into the
		// implicit position, decrementing the binary32 exponent as we go.
		e := uint32(127 - 15 + 1)
		for mant&0x400 == 0 {
			mant <<= 1
			e--
		}
		return math.Float32frombits(sign | e<<23 | (mant&0x3ff)<<13)
	case 0x1f:
		// ±Inf or NaN (payload kept in the high mantissa bits).
		return math.Float32frombits(sign | 0xff<<23 | mant<<13)
	default:
		return math.Float32frombits(sign | (exp+127-15)<<23 | mant<<13)
	}
}

// F32ToF16 encodes a float32 as a binary16 bit pattern with round-to-nearest,
// ties-to-even — the rounding numpy's npy_float_to_half (and the F16C/NEON
// hardware conversions numpy may dispatch to) performs. Overflow yields a
// signed infinity, values below the smallest binary16 subnormal round to a
// signed zero, and NaN stays NaN.
//
// Exported so the test helper can construct synthetic tables.
func F32ToF16(f float32) uint16 { return f32ToF16(f) }

// f32ToF16 is the unexported alias used inside the production code.
func f32ToF16(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int(b>>23) & 0xff
	mant := b & 0x7fffff
	switch {
	case exp == 0xff:
		if mant == 0 {
			return sign | 0x7c00 // ±Inf
		}
		m := uint16(mant >> 13)
		if m == 0 {
			m = 1 // keep it a NaN rather than collapsing to Inf
		}
		return sign | 0x7c00 | m
	case exp == 0:
		// A binary32 zero or subnormal (< 2^-126) is far below the binary16
		// subnormal range (2^-24): signed zero.
		return sign
	}
	he := exp - 127 + 15 // binary16 biased exponent
	if he >= 0x1f {
		return sign | 0x7c00 // overflow → ±Inf
	}
	if he >= 1 {
		// Normal binary16: drop 13 mantissa bits. A rounding carry out of the
		// mantissa propagates into the exponent field naturally (and into the
		// infinity encoding when he was 30 — the correct overflow result).
		return sign | uint16(roundShiftRNE(uint32(he)<<23|mant, 13))
	}
	if he < -10 {
		return sign // below half the smallest subnormal: rounds to zero
	}
	// Subnormal binary16: value = sig × 2^(he-1-23-... ); drop 13+(1-he) bits of
	// the 24-bit significand (implicit one included). A carry into bit 10 is the
	// smallest normal, which is again the correct result.
	sig := uint32(0x800000) | mant
	return sign | uint16(roundShiftRNE(sig, uint(13+(1-he))))
}

// roundShiftRNE returns v >> s rounded to nearest with ties to even.
func roundShiftRNE(v uint32, s uint) uint32 {
	half := uint32(1) << (s - 1)
	rem := v & (uint32(1)<<s - 1)
	q := v >> s
	if rem > half || (rem == half && q&1 == 1) {
		q++
	}
	return q
}

// roundF16 rounds a float32 to the nearest binary16 value and returns it as a
// float32 — the "store into a float16 array" step of the numpy reference.
func roundF16(f float32) float32 { return f16ToF32(f32ToF16(f)) }
