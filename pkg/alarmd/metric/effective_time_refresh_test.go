// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// Every result is exported from the start and reads the cache's counts at
// scrape.
func TestEffectiveTimeRefreshesAreReadAtScrape(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	read := func() map[string]float64 {
		out := map[string]float64{}
		for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_effective_time_refresh_total") {
			out[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return out
	}
	if got := read(); len(got) != len(strategycache.EffectiveTimeRefreshResults) || got["ok"] != 0 {
		t.Fatalf("unbound = %v, want every result at zero", got)
	}
	r.SetEffectiveTimeRefreshSource(func() map[string]uint64 { return map[string]uint64{"ok": 5, "timeout": 2, "error": 1} })
	if got := read(); got["ok"] != 5 || got["timeout"] != 2 || got["error"] != 1 {
		t.Fatalf("bound = %v", got)
	}
}
