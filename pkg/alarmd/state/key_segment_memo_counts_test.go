package state

import (
	"strconv"
	"testing"
)

// The memo drops everything when it fills, because its design assumes the
// populations it holds are bounded by configuration and reaching the bound
// means that assumption no longer holds. Nothing counted it, so the assumption
// could be false continuously and look exactly like a memo that never filled -
// and it is not equally safe for both domains: a state generation advances
// with every publish and is bounded by nothing.
func TestKeySegmentMemoCountsTheClearThatMeansItsPremiseFailed(t *testing.T) {
	memo := newKeySegmentMemo("alarmd-test-domain")

	for index := 0; index < keySegmentMemoEntries; index++ {
		memo.digest(strconv.Itoa(index))
	}
	if counts := memo.counts(); counts.Clears != 0 || counts.Misses != uint64(keySegmentMemoEntries) {
		t.Fatalf("counts=%+v, want the bound filled exactly without a clear", counts)
	}

	// Repeating a remembered value must not move it toward the bound.
	memo.digest("0")
	if counts := memo.counts(); counts.Hits != 1 || counts.Clears != 0 {
		t.Fatalf("counts=%+v, want one hit and still no clear", counts)
	}

	// One value past the bound, and the whole table goes.
	memo.digest(strconv.Itoa(keySegmentMemoEntries))
	counts := memo.counts()
	if counts.Clears != 1 {
		t.Fatalf("counts=%+v, want exactly one clear", counts)
	}
	if counts.Entries != 1 {
		t.Fatalf("counts=%+v, want the value that triggered the clear kept", counts)
	}
	if counts.Domain != "alarmd-test-domain" {
		t.Fatalf("counts=%+v, want the domain reported so the two can be told apart", counts)
	}
}

// Both domains report, and apart. Reporting them summed would hide the case
// the counter exists for: the tenant domain behaving and the generation domain
// clearing.
func TestKeySegmentMemoCountsReportBothDomainsSeparately(t *testing.T) {
	counts := KeySegmentMemoCounts()
	if len(counts) != 2 || counts[0].Domain == counts[1].Domain {
		t.Fatalf("counts=%+v, want the two domains reported apart", counts)
	}
	for _, one := range counts {
		if one.Domain == "" {
			t.Fatalf("counts=%+v, want every domain named", counts)
		}
	}
}
