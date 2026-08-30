package model2vec

import "sort"

// Hangul syllable decomposition constants (Unicode §3.12).
const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulTCount = 28
	hangulNCount = 21 * hangulTCount // VCount × TCount
	hangulSCount = 19 * hangulNCount // LCount × NCount
)

// appendNFD appends the full canonical decomposition of r to dst: Hangul
// syllables algorithmically, everything else through the generated table
// (nfd_table.go, full recursive decompositions). A rune without a canonical
// decomposition is appended unchanged.
//
// Canonical REORDERING of combining marks is deliberately not performed: the
// only consumer strips every nonspacing mark immediately afterwards, and the
// order among characters that survive is unaffected. PINNED.md lists this as a
// documented limit.
func appendNFD(dst []rune, r rune) []rune {
	if s := r - hangulSBase; s >= 0 && s < hangulSCount {
		dst = append(dst, hangulLBase+s/hangulNCount, hangulVBase+(s%hangulNCount)/hangulTCount)
		if t := s % hangulTCount; t != 0 {
			dst = append(dst, hangulTBase+t)
		}
		return dst
	}
	i := sort.Search(len(nfdKeys), func(i int) bool { return nfdKeys[i] >= r })
	if i < len(nfdKeys) && nfdKeys[i] == r {
		for _, d := range nfdValues[i] {
			dst = append(dst, d)
		}
		return dst
	}
	return append(dst, r)
}
