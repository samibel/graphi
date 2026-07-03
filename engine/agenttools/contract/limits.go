package contract

import (
	"errors"
	"sort"
)

// ApplyItemCap truncates items to the given cap after deterministic sorting.
// It returns a copy of the result with updated Limits.
func ApplyItemCap(r *Result, itemCap int) (*Result, error) {
	if r == nil {
		return nil, nil
	}
	if itemCap <= 0 {
		return nil, errors.New("item cap must be positive")
	}
	out := *r
	sortItems(out.Items)
	total := len(out.Items)
	if total > itemCap {
		out.Items = out.Items[:itemCap]
	}
	out.Limits = Limits{
		CapApplied:     itemCap,
		TotalAvailable: total,
		Dropped:        total - len(out.Items),
		Truncated:      total > itemCap,
		Next:           "",
	}
	return &out, nil
}

// ApplyByteCap truncates items after deterministic sorting so that the final
// serialized payload fits within byteCap. If even an empty result exceeds the
// cap (which should not happen for any sane cap), the oversized items are
// dropped and the result is marked truncated.
func ApplyByteCap(r *Result, byteCap int) (*Result, error) {
	if r == nil {
		return nil, nil
	}
	if byteCap <= 0 {
		return nil, errors.New("byte cap must be positive")
	}
	out := *r
	sortItems(out.Items)
	total := len(out.Items)
	for i := total; i >= 0; i-- {
		candidate := out
		candidate.Items = out.Items[:i]
		b, err := Serialize(&candidate)
		if err != nil {
			return nil, err
		}
		if len(b) <= byteCap {
			candidate.Limits = Limits{
				CapApplied:     byteCap,
				TotalAvailable: total,
				Dropped:        total - i,
				Truncated:      total > i,
				Next:           "",
			}
			return &candidate, nil
		}
	}
	// Should not be reached: an empty result with limits is small.
	out.Limits = Limits{
		CapApplied:     byteCap,
		TotalAvailable: total,
		Dropped:        total,
		Truncated:      true,
		Next:           "",
	}
	out.Items = nil
	return &out, nil
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Rank != items[j].Rank {
			return items[i].Rank > items[j].Rank // higher rank first
		}
		return items[i].RefID < items[j].RefID
	})
}
