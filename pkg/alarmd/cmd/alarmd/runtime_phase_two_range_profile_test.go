package main

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

func TestExpiredRangeProfileTracksResolvedFlagOnly(t *testing.T) {
	cfg := config.Default()
	disabled, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Capacity.ExpiredRangeEnabled {
		t.Fatal("default range enabled")
	}
	cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = true
	enabled, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Capacity.ExpiredRangeEnabled || enabled.Digest == disabled.Digest {
		t.Fatal("resolved switch absent from capacity digest")
	}
	wire, err := json.Marshal(enabled.Capacity)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["expired_range_enabled"] != true {
		t.Fatalf("safe resolved flag not exported: %s", wire)
	}
	enabled.Capacity.ExpiredRangeEnabled = false
	if enabled.Capacity != disabled.Capacity {
		t.Fatal("enabling changed unrelated resource capacity")
	}
}
