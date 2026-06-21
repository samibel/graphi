//go:build ignore

package fixture

func lambda() int    { return kappa(4) + mu() }
func mu() int        { return nu() }
func nu() int        { return 42 }
func xi(a, b int) int { return a*b + lambda() }
func omicron() int   { return xi(2, 3) }
