// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The server's account maps onto the fleet's shape field for field -- every
// number under its own key, the lagging Workers in the server's order with
// their connection and failure -- and the sentence is composed from it. A
// runtime without the stream publishes nothing.
func TestTheViewStreamAccountReachesTheFleetFieldForField(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	stats := viewstream.Stats{
		Leading: true, ControlEpoch: 7, Revision: 12, Sessions: 63,
		Counts:      viewstream.Counts{Expected: 64, Sent: 64, Acked: 63, Installed: 62, Switched: 0},
		Ignored:     viewstream.Ignored{UnknownVersion: 1, UnexpectedReceiver: 2, DigestMismatch: 3, StaleIncarnation: 4},
		NotSwitched: []viewstream.LaggingReceiver{{WorkerID: "w05", Incarnation: "i-05", Connected: true, SwitchedQueryGroups: 597}},
		Lagging: []viewstream.LaggingReceiver{
			{WorkerID: "w17", Incarnation: "i-17", Connected: false},
			{WorkerID: "w23", Incarnation: "i-23", Failure: "DELTA_DIGEST_MISMATCH", Connected: true},
			{WorkerID: "w31", Incarnation: "i-31", Connected: true},
		},
		// The installed Workers' word on their objects: sixty probed, one
		// of them missing forty; one could not probe, and is named.
		Objects:      viewstream.ObjectsSummary{Probed: 60, Unprobed: 1, Missing: 40, UnprobedWorkers: []string{"w40"}},
		Publications: 100, PublicationsSkipped: 5, SnapshotChunksSent: 20, DeltasSent: 80, EmptyDeltasSent: 60, DeltasOversized: 7, Refusals: 1,
	}
	want := &fleet.ViewStreamFacts{
		At: at, Leading: true, ControlEpoch: 7, Revision: 12, Sessions: 63,
		Expected: 64, Sent: 64, Acked: 63, Installed: 62, Switched: 0,
		Ignored:     fleet.ViewStreamIgnored{UnknownVersion: 1, UnexpectedReceiver: 2, DigestMismatch: 3, StaleIncarnation: 4},
		NotSwitched: []fleet.ViewStreamLagging{{WorkerID: "w05", Incarnation: "i-05", Connected: true, SwitchedQueryGroups: 597}},
		Lagging: []fleet.ViewStreamLagging{
			{WorkerID: "w17", Incarnation: "i-17", Connected: false},
			{WorkerID: "w23", Incarnation: "i-23", Failure: "DELTA_DIGEST_MISMATCH", Connected: true},
			{WorkerID: "w31", Incarnation: "i-31", Connected: true},
		},
		Objects:      fleet.ViewStreamObjects{Probed: 60, Unprobed: 1, Missing: 40, UnprobedWorkers: []string{"w40"}},
		Publications: 100, PublicationsSkipped: 5, SnapshotChunksSent: 20, DeltasSent: 80, EmptyDeltasSent: 60, DeltasOversized: 7, Refusals: 1,
		Line: "视图已装载 62/64，版本 12；缺对象 40（60 个副本探到）；1 个副本未探到对象（w40）；落后：w17（未连接）、w23（DELTA_DIGEST_MISMATCH） 等 3 个",
	}
	got := viewStreamFacts(stats, at)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("viewStreamFacts() = %+v\nwant %+v", got, want)
	}
	// On the wire the lagging rows carry no object fields -- a Worker short
	// of installed has probed nothing -- and the objects clause names the
	// Worker that could not probe as a list, empty rather than null when
	// every Worker probed.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Lagging []map[string]any `json:"lagging"`
		Objects map[string]any   `json:"objects"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, row := range wire.Lagging {
		if _, present := row["objects_probed"]; present {
			t.Fatalf("a lagging row carries objects_probed, a probe that never happened: %s", encoded)
		}
	}
	if wire.Objects["probed"] != 60.0 || wire.Objects["unprobed"] != 1.0 || wire.Objects["missing"] != 40.0 ||
		!reflect.DeepEqual(wire.Objects["unprobed_workers"], []any{"w40"}) {
		t.Fatalf("objects on the wire = %v, want probed 60 / unprobed 1 / missing 40 / unprobed_workers [w40]", wire.Objects)
	}
	allProbed := viewStreamFacts(viewstream.Stats{Leading: true, Counts: viewstream.Counts{Expected: 4, Sent: 4, Acked: 4, Installed: 4},
		Objects: viewstream.ObjectsSummary{Probed: 4}}, at)
	if allProbed.Objects.UnprobedWorkers == nil || allProbed.Line != "视图已装载 4/4，版本 0" {
		t.Fatalf("every Worker probed = %+v, want an empty list of unprobed Workers and a line with no objects clause", allProbed)
	}
	follower := viewStreamFacts(viewstream.Stats{Sessions: 0}, at)
	if follower.Leading || follower.Line != "非 leader，不服务视图流" || follower.Lagging == nil {
		t.Fatalf("a follower's account = %+v, want not leading with the sentence and an empty list", follower)
	}
	if viewStreamFleetFacts(nil, time.Now) != nil || viewStreamFleetFacts(func() viewstream.Stats { return stats }, nil) != nil {
		t.Fatal("a runtime without the stream or a clock publishes an account")
	}
	calls := 0
	source := viewStreamFleetFacts(func() viewstream.Stats { calls++; return stats }, func() time.Time { return at })
	if facts := source(); facts == nil || facts.Installed != 62 || calls != 1 {
		t.Fatalf("published account = %+v after %d reads, want the server's read once per snapshot", facts, calls)
	}
}
