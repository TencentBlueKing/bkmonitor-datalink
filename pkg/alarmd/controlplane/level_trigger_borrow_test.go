// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A level whose algorithms have no trigger of their own runs on the
// strategy's first trigger, the way the platform's trigger checker reads it,
// with the platform's default recovery; the level is named. A first trigger
// that cannot trigger is not lent, and a level with its own trigger is
// untouched.
func TestALevelWithoutItsOwnTriggerRunsOnTheFirstOne(t *testing.T) {
	const document = `{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a",` +
		`"query_configs":[{"agg_interval":60}],"algorithms":[{"level":2,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}]}],` +
		`"detects":[%s]}`
	const allDay = `"uptime":{"calendars":[],"time_ranges":[{"start":"09:00","end":"18:00"}]}`
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(t *testing.T, detects string) controlplane.Catalog {
		t.Helper()
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(fmt.Sprintf(document, detects)), Identity: identity}},
			Planner:    &recordingPlanner{facts: queryFacts(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	levelOf := func(t *testing.T, catalog controlplane.Catalog) (contract.LevelIRV2, map[string]any, map[string]any) {
		t.Helper()
		if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 || len(catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels) != 1 {
			t.Fatalf("strategy did not become a one-level Plan: %#v", catalog.Dispositions)
		}
		level := catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0]
		var trigger, recovery map[string]any
		if json.Unmarshal(level.TriggerPlan.Config, &trigger) != nil || json.Unmarshal(level.RecoveryPlan.Config, &recovery) != nil {
			t.Fatalf("level plans: %s %s", level.TriggerPlan.Config, level.RecoveryPlan.Config)
		}
		return level, trigger, recovery
	}
	borrowedRecords := func(catalog controlplane.Catalog) []controlplane.ObjectDisposition {
		var records []controlplane.ObjectDisposition
		for _, disposition := range catalog.Dispositions {
			if disposition.Reason == controlplane.ReasonLevelTriggerBorrowed {
				records = append(records, disposition)
			}
		}
		return records
	}
	missing := func(t *testing.T, catalog controlplane.Catalog) {
		t.Helper()
		if len(catalog.QueryGroups) != 0 {
			t.Fatalf("a trigger that cannot trigger was lent: %#v", catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels)
		}
		for _, disposition := range catalog.Dispositions {
			if disposition.Reason == "TRIGGER_CONFIG_MISSING" && disposition.LevelID == 2 {
				return
			}
		}
		t.Fatalf("dispositions=%#v, want level 2 refused as TRIGGER_CONFIG_MISSING", catalog.Dispositions)
	}

	t.Run("the only trigger is at another level", func(t *testing.T) {
		catalog := build(t, `{"level":1,"trigger_config":{"count":1,"check_window":5,`+allDay+`},"recovery_config":{"check_window":3}}`)
		level, trigger, recovery := levelOf(t, catalog)
		if level.Definition.LevelID != 2 || level.Definition.Priority != 2 || level.Connector != contract.LevelConnectorAND {
			t.Fatalf("level=%+v, want level 2 with its own priority and connector", level.Definition)
		}
		if trigger["required_anomalies"] != float64(1) || trigger["window_size"] != float64(5) || trigger["uptime"] == nil {
			t.Fatalf("trigger=%v, want the first trigger's count, window and effective time", trigger)
		}
		// The platform's recovery finds none for the level and takes its
		// default; the first detect's 3 is not the level's.
		if recovery["consecutive_windows"] != float64(5) || recovery["enabled"] != true {
			t.Fatalf("recovery=%v, want the platform's default five windows", recovery)
		}
		want := controlplane.ObjectDisposition{SourceID: "1", Scope: "LEVEL", LevelID: 2, Disposition: controlplane.DispositionConfigNormalized,
			Reason: controlplane.ReasonLevelTriggerBorrowed, Detail: "trigger_from_level=1"}
		if records := borrowedRecords(catalog); len(records) != 1 || records[0] != want {
			t.Fatalf("records=%#v, want one %#v", records, want)
		}
	})

	t.Run("the first trigger in the document is the one lent", func(t *testing.T) {
		catalog := build(t, `{"level":3,"trigger_config":{"count":2,"check_window":4}},{"level":1,"trigger_config":{"count":1,"check_window":5}}`)
		_, trigger, _ := levelOf(t, catalog)
		if trigger["required_anomalies"] != float64(2) || trigger["window_size"] != float64(4) {
			t.Fatalf("trigger=%v, want level 3's, the first written", trigger)
		}
		if records := borrowedRecords(catalog); len(records) != 1 || records[0].Detail != "trigger_from_level=3" {
			t.Fatalf("records=%#v", records)
		}
	})

	t.Run("a first trigger that cannot trigger is not lent", func(t *testing.T) {
		missing(t, build(t, `{"level":1,"trigger_config":{"count":0,"check_window":5}},{"level":3,"trigger_config":{"count":1,"check_window":5}}`))
	})

	t.Run("a first trigger whose level is written twice is not lent", func(t *testing.T) {
		missing(t, build(t, `{"level":1,"trigger_config":{"count":1,"check_window":5}},{"level":1,"trigger_config":{"count":3,"check_window":5}}`))
	})

	t.Run("no trigger at all", func(t *testing.T) {
		missing(t, build(t, ``))
	})

	t.Run("a level with its own trigger is read as written", func(t *testing.T) {
		catalog := build(t, `{"level":2,"trigger_config":{"count":2,"check_window":6},"recovery_config":{"check_window":3}},{"level":1,"trigger_config":{"count":1,"check_window":5}}`)
		_, trigger, recovery := levelOf(t, catalog)
		if trigger["required_anomalies"] != float64(2) || trigger["window_size"] != float64(6) || recovery["consecutive_windows"] != float64(3) {
			t.Fatalf("trigger=%v recovery=%v, want level 2's own", trigger, recovery)
		}
		if records := borrowedRecords(catalog); len(records) != 0 {
			t.Fatalf("a level with its own trigger was named: %#v", records)
		}
	})

	key := controlplane.WithheldKey{Disposition: controlplane.DispositionConfigNormalized, Reason: controlplane.ReasonLevelTriggerBorrowed}
	if count, reported := controlplane.ComposeCatalog(build(t, `{"level":2,"trigger_config":{"count":1,"check_window":5}}`)).Withheld[key]; !reported || count != 0 {
		t.Fatalf("LEVEL_TRIGGER_BORROWED reported=%v count=%d, want a published zero", reported, count)
	}
}
