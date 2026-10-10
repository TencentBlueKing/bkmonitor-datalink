// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package legacyoutput

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// legacyFixtureAnomalies is the anomaly events of the Python adapter oracle,
// ready to convert, and the time the oracle was captured at.
func legacyFixtureAnomalies(t *testing.T) ([]contract.TriggerEventV1, time.Time) {
	t.Helper()
	raw, err := os.ReadFile("../kafka/testdata/python-legacy-adapter.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		PythonTime int64 `json:"python_time"`
		Params     struct {
			Tenant     string                     `json:"bk_tenant_id"`
			Biz        int64                      `json:"bk_biz_id"`
			Strategies map[string]json.RawMessage `json:"strategies"`
			Events     []struct {
				ID         string                     `json:"event_id"`
				Key        string                     `json:"strategy_key"`
				Kind       string                     `json:"event_kind"`
				Level      uint32                     `json:"primary_level_id"`
				Item       int64                      `json:"item_id"`
				Time       int64                      `json:"source_time"`
				Times      []int64                    `json:"anomaly_timestamps"`
				Dimensions map[string]json.RawMessage `json:"dimensions"`
				Fields     []string                   `json:"dimension_fields"`
				Values     map[string]json.RawMessage `json:"values"`
				Value      json.RawMessage            `json:"value"`
			} `json:"events"`
		} `json:"params"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var events []contract.TriggerEventV1
	for _, item := range fixture.Params.Events {
		if item.Kind != contract.TriggerEventAbnormal {
			continue
		}
		item.Values["value"] = item.Value
		var strategy struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(fixture.Params.Strategies[item.Key], &strategy); err != nil {
			t.Fatal(err)
		}
		events = append(events, contract.TriggerEventV1{
			EventID: item.ID, TenantID: fixture.Params.Tenant, BusinessID: strconv.FormatInt(fixture.Params.Biz, 10),
			PlanRef: contract.RuntimePlanRefV1{StrategyID: strconv.FormatInt(strategy.ID, 10)}, EventKind: item.Kind, PrimaryLevelID: item.Level,
			RecordRef: contract.TriggerRecordRefV1{SourceTime: item.Time, Dimensions: item.Dimensions},
			Observed:  contract.TriggerObservedV1{Values: item.Values},
			LegacyOutput: &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
				Strategy: fixture.Params.Strategies[item.Key], DimensionFields: item.Fields, ItemID: strconv.FormatInt(item.Item, 10),
			}), AnomalyTimestamps: item.Times},
		})
	}
	if len(events) == 0 {
		t.Fatal("the oracle has no anomaly event")
	}
	return events, time.Unix(fixture.PythonTime, 0)
}

// ConvertEach judges every event on its own account and writes the
// snapshots once: an event the converter refuses fails alone, a strategy
// whose frozen configuration cannot be prepared fails only its own events,
// and only the strategies that converted an event have their snapshot
// written, in one write however many events were refused.
func TestConvertEachRefusesAloneAndWritesSnapshotsOnce(t *testing.T) {
	events, now := legacyFixtureAnomalies(t)
	refused := events[0]
	refused.EventID = "refused-event"
	refused.PrimaryLevelID = 9
	broken := events[0]
	broken.EventID = "broken-strategy-event"
	broken.PlanRef.StrategyID = "424242"
	broken.LegacyOutput = &contract.LegacyEventContext{
		Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
			Strategy: json.RawMessage(`{"id":424242,"bk_biz_id":2,"update_time":1,"name":"no items"}`), DimensionFields: []string{"host"}, ItemID: "1",
		}),
		AnomalyTimestamps: events[0].LegacyOutput.AnomalyTimestamps,
	}
	batch := []contract.TriggerEventV1{events[0], refused, broken}
	store := &snapshotRecorder{}
	converter := Converter{Store: store, Now: func() time.Time { return now }}

	converted, failures, err := converter.ConvertEach(context.Background(), batch)
	if err != nil {
		t.Fatalf("ConvertEach() error = %v, want the refusals per event", err)
	}
	if len(converted) != len(batch) || len(failures) != len(batch) {
		t.Fatalf("results %d / failures %d, want both aligned with the %d events", len(converted), len(failures), len(batch))
	}
	if failures[0] != nil || converted[0].EventID != events[0].EventID || len(converted[0].Payload) == 0 {
		t.Fatalf("event 0 = %+v / %v, want it converted", converted[0], failures[0])
	}
	if failures[1] == nil || failures[2] == nil {
		t.Fatalf("failures = %v, want the refused event and the broken strategy's event to fail", failures)
	}
	// A level the strategy has no severity for, and a strategy too incomplete
	// to convert against, are the strategy's to fix.
	var config *StrategyConfigError
	if !errors.As(failures[1], &config) || !errors.As(failures[2], &config) {
		t.Fatalf("failures = %v, want both named as the strategy configuration's", failures)
	}
	if store.batches != 1 || len(store.snapshots) != 1 || strconv.FormatInt(store.snapshots[0].StrategyID, 10) != events[0].PlanRef.StrategyID {
		t.Fatalf("snapshot writes = %d with %+v, want one write holding only the strategy that converted an event", store.batches, store.snapshots)
	}

	// The same batch through ConvertBatch keeps its whole-batch contract: one
	// refusal fails the batch and nothing is written.
	store = &snapshotRecorder{}
	converter.Store = store
	if _, err := converter.ConvertBatch(context.Background(), batch); err == nil || store.batches != 0 {
		t.Fatalf("ConvertBatch() = %v with %d writes, want the batch refused before any write", err, store.batches)
	}
}

// The snapshot write is the one failure that says nothing about the events,
// and it comes back as such rather than as a refusal of each.
func TestConvertEachReturnsTheSnapshotStoreFailureAlone(t *testing.T) {
	events, now := legacyFixtureAnomalies(t)
	store := &snapshotRecorder{err: errors.New("store down")}
	converter := Converter{Store: store, Now: func() time.Time { return now }}
	converted, failures, err := converter.ConvertEach(context.Background(), events)
	var storeErr *SnapshotStoreError
	if !errors.As(err, &storeErr) || converted != nil || failures != nil {
		t.Fatalf("ConvertEach() = %v, %v, %v; want only a SnapshotStoreError", converted, failures, err)
	}
}

// What only alarmd gets wrong -- an event routed here without its frozen
// context, a strategy identity that is not the event's, a kind the protocol
// does not carry -- is not named as the strategy's.
func TestConvertEachLeavesAlarmdsOwnRefusalsUnnamedAsConfiguration(t *testing.T) {
	events, now := legacyFixtureAnomalies(t)
	noContext, mismatch, recovery, missingItem := events[0], events[0], events[0], events[0]
	noContext.EventID, noContext.LegacyOutput = "no-context", nil
	mismatch.EventID, mismatch.PlanRef.StrategyID = "mismatch", "424242"
	recovery.EventID, recovery.EventKind = "recovery", contract.TriggerEventRecovery
	// The frozen item id names an item its own frozen strategy does not hold.
	missingItem.EventID = "missing-item"
	frozen := *events[0].LegacyOutput
	frozen.Configuration = contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
		Strategy: events[0].LegacyOutput.Configuration.StrategyJSON(), DimensionFields: events[0].LegacyOutput.Configuration.DimensionFields(), ItemID: "987654321",
	})
	missingItem.LegacyOutput = &frozen
	converter := Converter{Store: &snapshotRecorder{}, Now: func() time.Time { return now }}
	_, failures, err := converter.ConvertEach(context.Background(), []contract.TriggerEventV1{noContext, mismatch, recovery, missingItem})
	if err != nil {
		t.Fatal(err)
	}
	for index, failure := range failures {
		var config *StrategyConfigError
		if failure == nil || errors.As(failure, &config) {
			t.Fatalf("event %d: %v, want refused and not as the strategy's", index, failure)
		}
	}
	if !strings.Contains(failures[3].Error(), "not in its frozen strategy") {
		t.Fatalf("the missing item was refused for another reason: %v", failures[3])
	}
}

// An item the strategy holds with no name is the strategy as configured.
func TestConvertEachNamesAnUnnamedItemAsTheStrategys(t *testing.T) {
	events, now := legacyFixtureAnomalies(t)
	var strategy map[string]any
	if err := json.Unmarshal(events[0].LegacyOutput.Configuration.StrategyJSON(), &strategy); err != nil {
		t.Fatal(err)
	}
	for _, raw := range strategy["items"].([]any) {
		raw.(map[string]any)["name"] = ""
	}
	unnamed, _ := json.Marshal(strategy)
	event := events[0]
	frozen := *event.LegacyOutput
	frozen.Configuration = contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
		Strategy: unnamed, DimensionFields: event.LegacyOutput.Configuration.DimensionFields(), ItemID: event.LegacyOutput.Configuration.ItemID(),
	})
	event.LegacyOutput = &frozen
	converter := Converter{Store: &snapshotRecorder{}, Now: func() time.Time { return now }}
	_, failures, err := converter.ConvertEach(context.Background(), []contract.TriggerEventV1{event})
	var config *StrategyConfigError
	if err != nil || !errors.As(failures[0], &config) || !strings.Contains(failures[0].Error(), "has no name") {
		t.Fatalf("an unnamed item: %v, %v", failures, err)
	}
}
