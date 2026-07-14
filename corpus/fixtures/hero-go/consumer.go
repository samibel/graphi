package hero

// Publish parses and renders in one step.
func Publish(raw string) string {
	d := Parse(raw)
	return Render(d)
}

// Preview is a read-only view over Parse.
func Preview(raw string) string { return Parse(raw).Body }
