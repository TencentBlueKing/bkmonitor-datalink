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
		Counts:  viewstream.Counts{Expected: 64, Sent: 64, Acked: 63, Installed: 62, Switched: 0},
		Ignored: viewstream.Ignored{UnknownVersion: 1, UnexpectedReceiver: 2, DigestMismatch: 3, StaleIncarnation: 4},
		Lagging: []viewstream.LaggingReceiver{
			{WorkerID: "w17", Incarnation: "i-17", ObjectsMissing: 40, Connected: false},
			{WorkerID: "w23", Incarnation: "i-23", Failure: "DELTA_DIGEST_MISMATCH", ObjectsMissing: 2, Connected: true},
		},
		Publications: 100, PublicationsSkipped: 5, SnapshotChunksSent: 20, DeltasSent: 80, EmptyDeltasSent: 60, Refusals: 1,
	}
	want := &fleet.ViewStreamFacts{
		At: at, Leading: true, ControlEpoch: 7, Revision: 12, Sessions: 63,
		Expected: 64, Sent: 64, Acked: 63, Installed: 62, Switched: 0,
		Ignored: fleet.ViewStreamIgnored{UnknownVersion: 1, UnexpectedReceiver: 2, DigestMismatch: 3, StaleIncarnation: 4},
		Lagging: []fleet.ViewStreamLagging{
			{WorkerID: "w17", Incarnation: "i-17", ObjectsMissing: 40, Connected: false},
			{WorkerID: "w23", Incarnation: "i-23", Failure: "DELTA_DIGEST_MISMATCH", ObjectsMissing: 2, Connected: true},
		},
		Publications: 100, PublicationsSkipped: 5, SnapshotChunksSent: 20, DeltasSent: 80, EmptyDeltasSent: 60, Refusals: 1,
		Line: "视图已装载 62/64，版本 12；落后：w17（未连接）、w23（DELTA_DIGEST_MISMATCH）",
	}
	if got := viewStreamFacts(stats, at); !reflect.DeepEqual(got, want) {
		t.Fatalf("viewStreamFacts() = %+v\nwant %+v", got, want)
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
