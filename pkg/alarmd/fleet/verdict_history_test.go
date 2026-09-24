// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"
	"time"
)

// The record starts with the first verdict decided, keeps a verdict only
// when it changes, carries what decided each change, and stays bounded,
// oldest first.
func TestTheVerdictRecordKeepsEachChangeAndWhatDecidedIt(t *testing.T) {
	service := &Service{}
	at := time.Date(2026, 9, 24, 4, 0, 0, 0, time.UTC)
	healthy := &View{Health: HealthHealthy, Covered: 51, Determined: 51}
	degraded := &View{Health: HealthDegraded, Covered: 51, Determined: 51,
		Degradations: []Degradation{{Kind: DegradationOpenAlertSetStale, Replica: "a"}, {Kind: DegradationOpenAlertSetStale, Replica: "b"}, {Kind: DegradationControlSourceStale, Replica: "a"}},
		Gaps:         []Gap{{Kind: GapReplicaMissing, Replica: "c"}}}
	service.RecordVerdict(healthy, at)
	service.RecordVerdict(healthy, at.Add(time.Minute))
	service.RecordVerdict(degraded, at.Add(2*time.Minute))
	changes, since := service.VerdictHistory()
	if !since.Equal(at) || len(changes) != 2 {
		t.Fatalf("since %v changes %+v, want the first verdict and one change", since, changes)
	}
	if changes[0].From != "" || changes[0].To != HealthHealthy || changes[0].Covered != 51 {
		t.Fatalf("first entry %+v", changes[0])
	}
	change := changes[1]
	if change.From != HealthHealthy || change.To != HealthDegraded || !change.At.Equal(at.Add(2*time.Minute)) ||
		len(change.Degradations) != 2 || change.Degradations[0] != DegradationControlSourceStale || change.Degradations[1] != DegradationOpenAlertSetStale ||
		len(change.Gaps) != 1 || change.Gaps[0] != GapReplicaMissing {
		t.Fatalf("change %+v, want what decided it, each kind once", change)
	}
	// A decision that started before the last recorded one is dropped, not
	// recorded out of order.
	service.RecordVerdict(healthy, at.Add(time.Minute))
	if again, _ := service.VerdictHistory(); len(again) != 2 {
		t.Fatalf("a late decision was recorded: %+v", again)
	}
	for i := 0; i < MaxVerdictChanges+3; i++ {
		view := healthy
		if i%2 == 0 {
			view = degraded
		}
		service.RecordVerdict(view, at.Add(time.Duration(10+i)*time.Minute))
	}
	changes, _ = service.VerdictHistory()
	if len(changes) != MaxVerdictChanges {
		t.Fatalf("%d changes kept, want %d", len(changes), MaxVerdictChanges)
	}
	for i := 1; i < len(changes); i++ {
		if !changes[i].At.After(changes[i-1].At) {
			t.Fatalf("changes out of order at %d", i)
		}
	}
}

// The health route records the verdict it decides and carries the record.
func TestTheHealthRouteCarriesTheVerdictRecord(t *testing.T) {
	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()[:1]},
	)
	service.SetReplica("pod-a")
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, handler, "/api/health")
	if body["verdict_history_replica"] != "pod-a" {
		t.Fatalf("verdict_history_replica = %v, want whose record it is", body["verdict_history_replica"])
	}
	history, _ := body["verdict_history"].([]any)
	if len(history) != 1 || body["verdict_history_since"] == nil {
		t.Fatalf("verdict_history %v since %v, want the verdict just decided", body["verdict_history"], body["verdict_history_since"])
	}
	if first, _ := history[0].(map[string]any); first["to"] != body["health"] {
		t.Fatalf("recorded %v, decided %v", first["to"], body["health"])
	}
}
