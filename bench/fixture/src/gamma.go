//go:build ignore

package fixture

func eta() int   { return zeta(3) + theta() }
func theta() int { return iota() }
func iota() int  { return 7 }
func kappa(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += zeta(i)
	}
	return total
}
