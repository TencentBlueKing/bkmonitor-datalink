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
		for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_activation_rebuild_total") {
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
	if got := read(); got["rebuilt"] != 2 || got["conflict"] != 1 || got["timeline_missing"] != 0 || len(got) != len(controlplane.ActivationRebuildOutcomes) {
		t.Fatalf("after the source: %v", got)
	}
}
