package distill

// LoadDistilled parses a previously distilled artifact so a future
// resume_session flow can restore context. It is a thin alias for Unmarshal
// today but provides a stable seam for future versioning.
func LoadDistilled(data []byte) (DistilledSession, error) {
	return Unmarshal(data)
}
