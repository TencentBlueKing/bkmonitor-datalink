// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The alert reads the record's dimensions with the host overlay written
// over them, and so its fingerprint follows CMDB's address as the
// platform's does; without an overlay it reads the record's own map,
// unchanged and not copied. The record itself is never changed.
func TestTheAlertReadsTheHostOverlayOverTheRecordsDimensions(t *testing.T) {
	record := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"198.51.100.9"`),
		"bk_target_cloud_id": json.RawMessage(`"0"`), "bk_agent_id": json.RawMessage(`"agent-live"`)}
	if got := alertDimensions(record, nil); len(got) != len(record) || string(got["bk_target_ip"]) != `"198.51.100.9"` {
		t.Fatalf("without an overlay the alert read %v", got)
	}
	overlay := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.172"`), "bk_target_cloud_id": json.RawMessage(`"0"`),
		"bk_host_id": json.RawMessage(`"720002"`)}
	merged := alertDimensions(record, overlay)
	if string(merged["bk_target_ip"]) != `"192.0.2.172"` || string(merged["bk_host_id"]) != `"720002"` || string(merged["bk_agent_id"]) != `"agent-live"` {
		t.Fatalf("merged %v", merged)
	}
	if string(record["bk_target_ip"]) != `"198.51.100.9"` || len(record) != 3 {
		t.Fatalf("the record's own dimensions changed: %v", record)
	}
	identity := contract.MonitorOutputIdentity{DimensionFields: []string{"bk_target_ip", "bk_target_cloud_id", "bk_agent_id"}}
	own, err := contract.MonitorDedupeMD5("101", "2", record, identity)
	if err != nil {
		t.Fatal(err)
	}
	written, err := contract.MonitorDedupeMD5("101", "2", merged, identity)
	if err != nil {
		t.Fatal(err)
	}
	if own == written {
		t.Fatal("the overlay did not move the fingerprint to the host's address")
	}
}

// Evaluate carries the request's host overlay to the alert: the event's
// dimensions are the record's with the overlay over them and its
// fingerprint is the one those give, while the series identity stays the
// record's own. Without the overlay the same record is filed by its own
// dimensions.
func TestEvaluateFilesTheAlertUnderTheHostOverlay(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	native := func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &identity
	}
	overlay := map[string]json.RawMessage{"host": json.RawMessage(`"overlaid"`)}
	for _, arm := range []struct {
		name    string
		overlay map[string]json.RawMessage
	}{{"with an overlay", overlay}, {"without one", nil}} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevelsShaped(t, "50", "50", native)
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
				{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			}}}
			req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
			req.HostOverlay = arm.overlay
			result, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			plan0 := result.Plans[0]
			if len(plan0.StateResults) != 1 || len(plan0.StateResults[0].Events) != 1 {
				t.Fatalf("state results %+v, want one record with one event", plan0.StateResults)
			}
			event := plan0.StateResults[0].Events[0]
			record, ok := req.Inputs[0].Inputs[0].View.Record(0)
			if !ok {
				t.Fatal("the fixture has no record")
			}
			want := map[string]json.RawMessage{}
			for key, value := range record.Dimensions() {
				want[key] = value
			}
			for key, value := range arm.overlay {
				want[key] = value
			}
			if len(event.RecordRef.Dimensions) != len(want) {
				t.Fatalf("event dimensions %v, want %v", event.RecordRef.Dimensions, want)
			}
			for key, value := range want {
				if string(event.RecordRef.Dimensions[key]) != string(value) {
					t.Fatalf("event dimensions %v, want %v", event.RecordRef.Dimensions, want)
				}
			}
			fingerprint, err := contract.MonitorDedupeMD5("7", "2", want, identity)
			if err != nil {
				t.Fatal(err)
			}
			if event.DedupeMD5 != fingerprint {
				t.Fatalf("DedupeMD5 = %s, want %s from the record with the overlay over it", event.DedupeMD5, fingerprint)
			}
			if event.RecordRef.DimensionIdentityDigest != record.DimensionIdentityDigest() {
				t.Fatalf("series identity %s, want the record's own %s", event.RecordRef.DimensionIdentityDigest, record.DimensionIdentityDigest())
			}
		})
	}
}
