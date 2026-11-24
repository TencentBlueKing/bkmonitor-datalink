package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// Every rebuild outcome is a series from the first scrape, zero included, and
// the counts are the repository's own.
func TestActivationRebuildsAreScrapedByOutcomeFromZero(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	read := func() map[string]float64 {
		got := map[string]float64{}
		for _, m := range gatherFamily(t, r, "alarmd_activation_rebuild_total") {
			got[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return got
	}
	if got := read(); len(got) != len(controlplane.ActivationRebuildOutcomes) {
		t.Fatalf("before any source: %v, want every outcome at zero", got)
	}
	r.SetActivationRebuildSource(func() map[controlplane.ActivationRebuildOutcome]uint64 {
		return map[controlplane.ActivationRebuildOutcome]uint64{controlplane.ActivationRebuilt: 2, controlplane.ActivationRebuildConflict: 1}
	})
	if got := read(); got["rebuilt"] != 2 || got["conflict"] != 1 || got["coverage_invalid"] != 0 || len(got) != len(controlplane.ActivationRebuildOutcomes) {
		t.Fatalf("after the source: %v", got)
	}
}

// Every reason a rebuild kept a Query Group unread is a series from the first
// scrape, zero included, and the counts are the repository's own.
func TestActivationRebuildUnreadQueryGroupsAreScrapedByReasonFromZero(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	read := func() map[string]float64 {
		got := map[string]float64{}
		for _, m := range gatherFamily(t, r, "alarmd_activation_rebuild_unread_query_groups_total") {
			got[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return got
	}
	if got := read(); len(got) != 3 || got["absent"] != 0 || got["undecodable"] != 0 || got["newer_format"] != 0 {
		t.Fatalf("before any source: %v, want absent, undecodable and newer_format at zero", got)
	}
	r.SetActivationRebuildUnreadSource(func() map[controlplane.SkippedTimelineReason]uint64 {
		return map[controlplane.SkippedTimelineReason]uint64{controlplane.SkippedTimelineAbsent: 2, controlplane.SkippedTimelineUndecodable: 1}
	})
	if got := read(); got["absent"] != 2 || got["undecodable"] != 1 || got["newer_format"] != 0 || len(got) != 3 {
		t.Fatalf("after the source: %v", got)
	}
}
