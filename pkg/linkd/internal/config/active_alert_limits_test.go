// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package config

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

func testLimits() *ActiveAlertLimits {
	return &ActiveAlertLimits{Rules: []ActiveAlertLimitRule{{Name: "strategy", GroupBy: []string{"labels.strategy_id"}, MaxActive: 5000, Notify: &ActiveAlertLimitNotify{Channel: "changes", Fields: map[string]string{"bk_tenant_id": "bk_tenant_id", "strategy_id": "labels.strategy_id"}}}}}
}

func TestActiveLimitsDefaultsRoundTripAndClone(t *testing.T) {
	disabled := false
	source := EventSource{EventSourceID: "built_in_bk", ActiveAlertLimits: testLimits()}
	source.ActiveAlertLimits.Enabled = &disabled
	for _, codec := range []struct {
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{json.Marshal, json.Unmarshal}, {yaml.Marshal, yaml.Unmarshal}} {
		raw, err := codec.marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		var restored EventSource
		if err := codec.unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		got := restored.WithDefaults()
		c := got.ActiveAlertLimits
		if c.IsEnabled() || c.TimeoutMilliseconds != 100 || c.CountCacheTTLMilliseconds != 1000 || c.CountCacheMaxGroups != 4096 {
			t.Fatal(c)
		}
		c.Rules[0].GroupBy[0] = "subject_id"
		c.Rules[0].Notify.Fields["strategy_id"] = "subject_id"
		*c.Enabled = true
		if restored.ActiveAlertLimits.Rules[0].GroupBy[0] != "labels.strategy_id" || *restored.ActiveAlertLimits.Enabled {
			t.Fatal("clone changed input")
		}
	}
	if (EventSource{EventSourceID: "built_in_bk"}).WithDefaults().ActiveAlertLimits != nil {
		t.Fatal("worker defaults injected business rule")
	}
}

func TestActiveLimitsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ActiveAlertLimits)
	}{
		{"zero threshold", func(c *ActiveAlertLimits) { c.Rules[0].MaxActive = 0 }},
		{"unsupported field", func(c *ActiveAlertLimits) { c.Rules[0].GroupBy = []string{"content"} }},
		{"duplicate field", func(c *ActiveAlertLimits) { c.Rules[0].GroupBy = append(c.Rules[0].GroupBy, "labels.strategy_id") }},
		{"unknown notify field", func(c *ActiveAlertLimits) { c.Rules[0].Notify.Fields["x"] = "labels.other" }},
		{"negative timeout", func(c *ActiveAlertLimits) { c.TimeoutMilliseconds = -1 }},
		{"duplicate rule", func(c *ActiveAlertLimits) { c.Rules = append(c.Rules, c.Rules[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testLimits()
			tc.change(c)
			if c.Validate() == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	c := testLimits()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Rules[0].GroupBy = nil
	c.Rules[0].Notify = nil
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadActiveLimitsUsesExistingSourceImport(t *testing.T) {
	raw := `event_sources:
 - event_source_id: source
   enabled: true
   active_alert_limits:
     enabled: false
     rules:
      - name: source-total
        group_by: []
        max_active: 1000
   storage:
     type: kafka
     kafka: {brokers: ["kafka:9092"], topic: raw, consumer_group: cleaner}
`
	cfg, err := load(writeConfig(t, raw), Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventSources[0].ActiveAlertLimits.IsEnabled() || cfg.EventSources[0].ActiveAlertLimits.Rules[0].MaxActive != 1000 {
		t.Fatal("limit config lost")
	}
}
