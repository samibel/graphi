//go:build ignore

package fixture

func delta() int     { return 1 }
func epsilon() int   { return delta() + 2 }
func zeta(x int) int { return x*x + delta() }
