// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"encoding/json"
	"testing"
)

func refreshEvent(t *testing.T, facts *SourceRefreshFacts) map[string]any {
	t.Helper()
	var output bytes.Buffer
	renderTo(&output, Observation{
		Component: ComponentControlPlane, Stage: StageSnapshotRefreshed, Result: ResultSuccess,
		SourceRefresh: facts,
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode source refresh log: %v; log=%s", err, output.String())
	}
	return event
}

// A round that found no activation beside an earlier publication and
// established one says so on its refresh line, with the publication it
// activated and every Query Group counted as added.
func TestARebuiltActivationTravelsOnTheRefreshLine(t *testing.T) {
	event := refreshEvent(t, &SourceRefreshFacts{
		Status: SourceRefreshUnchanged, ObservationID: "observation-1",
		SnapshotRevision: "snapshot-1", PublicationEpoch: 7, ActivationRebuilt: true,
		CountsKnown: true, NewQueryGroups: 3, AddedQueryGroups: 3,
	})
	if event["activation_rebuilt"] != true || event["snapshot_revision"] != "snapshot-1" ||
		event["publication_epoch"] != float64(7) || event["added_query_groups"] != float64(3) {
		t.Fatalf("rebuilt activation on the refresh line = %#v", event)
	}
	if _, present := refreshEvent(t, &SourceRefreshFacts{Status: SourceRefreshUnchanged})["activation_rebuilt"]; present {
		t.Fatal("a round that rebuilt nothing logged activation_rebuilt")
	}
}

// How a round read its source travels on the refresh line: mode and reason,
// how many documents it asked for, and the change signal's age only when the
// round found a signal, so an absent signal is not logged as an age of zero.
func TestHowTheRoundReadItsSourceTravelsOnTheRefreshLine(t *testing.T) {
	event := refreshEvent(t, &SourceRefreshFacts{
		Status: SourceRefreshUnchanged, ObservationID: "observation-steady",
		ReadMode: SourceReadSkipped, ReadReason: SourceReadUnchanged,
		ChangeSignalPresent: true, ChangeSignalAgeSeconds: 75,
	})
	if event["source_read_mode"] != "skipped" || event["source_read_reason"] != "unchanged" ||
		event["source_strategies_read"] != float64(0) || event["source_change_signal_present"] != true ||
		event["source_change_signal_age_seconds"] != float64(75) {
		t.Fatalf("skipped round on the refresh line = %#v", event)
	}
	event = refreshEvent(t, &SourceRefreshFacts{
		Status: SourceRefreshUnchanged, ReadMode: SourceReadFull, ReadReason: SourceReadMissing, StrategiesRead: 943,
	})
	if event["source_read_mode"] != "full" || event["source_read_reason"] != "missing" ||
		event["source_strategies_read"] != float64(943) || event["source_change_signal_present"] != false {
		t.Fatalf("round without a signal on the refresh line = %#v", event)
	}
	if _, present := event["source_change_signal_age_seconds"]; present {
		t.Fatalf("a round that found no signal logged an age: %#v", event["source_change_signal_age_seconds"])
	}
}
