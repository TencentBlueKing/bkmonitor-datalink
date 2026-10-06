package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"gopkg.in/yaml.v3"
)

func TestFTAStorageRoutingYAMLAndFrozenRuntimeFacts(t *testing.T) {
	var cfg config.Config
	err := yaml.Unmarshal([]byte(`phase_two:
  control:
    legacy_query_runtime:
      fta_event_storage:
        table_id: fta.events
        storage_id: "17"
        storage_type: elasticsearch
        db: bkfta_event_*_read
        measurement: __default__
        need_add_time: false
        time_field:
          name: time
          type: date
          unit: millisecond
        source_type: event
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	route := cfg.PhaseTwo.Control.LegacyQueryRuntime.FTAEventStorage
	if route == nil || route.StorageID != "17" || route.TimeField.Unit != "millisecond" || route.DB != "bkfta_event_*_read" || route.TimeField.Name != "time" {
		t.Fatalf("route=%+v", route)
	}
	facts := legacyQueryRuntimeFacts(cfg, platformsettings.CodeDefaults())
	route.DB = "mutated"
	if facts.FTAEventStorage == route || facts.FTAEventStorage.DB != "bkfta_event_*_read" {
		t.Fatal("runtime facts retain mutable config pointer")
	}
}
