package price

import "fmt"

// FormatUSD renders a MicroUSD value as a dollar string for DISPLAY ONLY. It is
// a presentation concern; all arithmetic stays in MicroUSD. Rounding here is
// explicit and ROUND-DOWN (toward zero) so a displayed figure never exceeds the
// exact integer value — never inflating the savings claim.
func FormatUSD(v MicroUSD) string {
	neg := v < 0
	if neg {
		v = -v
	}
	dollars := v / USDPerMicro
	cents := (v % USDPerMicro) / 10_000 // 1e6 micro -> 100 cents; integer truncation toward zero
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s$%d.%02d", sign, dollars, cents)
}
