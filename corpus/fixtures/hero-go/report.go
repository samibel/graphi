package hero

// Report formats a document body with a heading.
func Report(raw string) string {
	return "# " + Render(Parse(raw))
}
