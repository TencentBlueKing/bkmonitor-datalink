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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// A window's output has to come back for the object it was recorded for, and
// come back unaltered. Writing only to the container log means whoever opened
// the window has to know to go and search a log index, which the page never
// mentions.
func TestDiagnosticLoadReadsThatObjectsRecordsUnaltered(t *testing.T) {
	client := &fakeDiagnosticRedis{lists: map[string][]string{
		"alarmd:test:diag:v1:qg-a": {`{"stage":"runner_decision","query_group":"qg-a"}`},
		"alarmd:test:diag:v1:qg-b": {`{"stage":"runner_decision","query_group":"qg-b"}`},
	}}
	store, err := NewDiagnosticStore(client, "alarmd:test")
	if err != nil || store == nil {
		t.Fatalf("NewDiagnosticStore() = %v, %v", store, err)
	}

	records, err := store.Load(context.Background(), "qg-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || string(records[0]) != `{"stage":"runner_decision","query_group":"qg-a"}` {
		t.Fatalf("records = %v, want only qg-a's own record, unaltered", records)
	}
	// The key carries the object, so one object's window cannot show another's
	// records -- the reason a window is opened is to look at one object.
	if client.lastKey != "alarmd:test:diag:v1:qg-a" {
		t.Fatalf("read key = %q, want the requested object's own", client.lastKey)
	}
	if _, err := store.Load(context.Background(), "", 0); err == nil {
		t.Fatal("a read without an object was accepted")
	}
}

// This path must never be able to slow down or fail the execution it observes,
// so a full buffer drops and counts rather than waiting for room.
func TestDiagnosticRecordNeverBlocksAndCountsWhatItDropped(t *testing.T) {
	store := &DiagnosticStore{records: make(chan diagnosticWrite, 1)}
	store.Record("qg", []byte(`{"a":1}`))
	// Nothing is draining, so everything past the buffer is dropped. The call
	// returning at all is the assertion: a blocking write here would stall the
	// pipeline behind a diagnostic.
	for i := 0; i < 10; i++ {
		store.Record("qg", []byte(`{"a":1}`))
	}
	if got := store.Health().Dropped; got != 10 {
		t.Fatalf("dropped = %d, want the 10 that did not fit", got)
	}
}

// An empty list and an unrecorded window look identical, and they call for
// opposite next steps, so the counts travel with the records.
func TestDiagnosticHealthSeparatesNothingHappenedFromNothingRecorded(t *testing.T) {
	store := &DiagnosticStore{records: make(chan diagnosticWrite, 1)}
	if health := store.Health(); health.Written != 0 || health.Dropped != 0 || health.Failed != 0 {
		t.Fatalf("a fresh store reported activity: %+v", health)
	}
	store.dropped.Add(3)
	store.failed.Add(2)
	store.written.Add(7)
	health := store.Health()
	if health.Dropped != 3 || health.Failed != 2 || health.Written != 7 {
		t.Fatalf("health did not carry what happened: %+v", health)
	}
}

// A nil client is how a deployment that has not wired diagnostics behaves. It
// must be a disabled feature rather than an error, so windows still work and
// only the read-back is missing.
func TestDiagnosticStoreIsDisabledRatherThanBrokenWithoutAClient(t *testing.T) {
	store, err := NewDiagnosticStore(nil, "alarmd:test")
	if err != nil || store != nil {
		t.Fatalf("NewDiagnosticStore(nil) = %v, %v; want a disabled store and no error", store, err)
	}
	// Every method has to tolerate the disabled store, because the caller holds
	// a nil pointer rather than a flag.
	store.Record("qg", []byte(`{}`))
	store.Run(context.Background())
	if health := store.Health(); health != (DiagnosticHealth{}) {
		t.Fatalf("disabled store reported health: %+v", health)
	}
	if _, err := store.Load(context.Background(), "qg", 0); err == nil {
		t.Fatal("a disabled store answered a read as if it had records")
	}
}

// Only LRange is answered; any other command would panic and thereby prove the
// read path reached something it should not use.
type fakeDiagnosticRedis struct {
	redis.Cmdable
	lists   map[string][]string
	lastKey string
}

func (fake *fakeDiagnosticRedis) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	fake.lastKey = key
	command := redis.NewStringSliceCmd(ctx)
	command.SetVal(fake.lists[key])
	return command
}

// The API is capped at five capabilities so this page cannot grow into a
// service that needs maintaining. Reading what a window recorded is part of
// looking at one object, so it rides on the object rather than opening a sixth
// route -- and it stays opt-in, so a reader who did not ask does not pay for it.
func TestObjectDetailCarriesWindowRecordsOnlyWhenAsked(t *testing.T) {
	client := &fakeDiagnosticRedis{lists: map[string][]string{
		"alarmd:test:diag:v1:qg-a": {`{"stage":"runner_decision"}`},
	}}
	store, err := NewDiagnosticStore(client, "alarmd:test")
	if err != nil {
		t.Fatal(err)
	}
	handler := diagnosticsTestHandler(t, store)

	plain := requestJSON(t, handler, "/api/objects/qg-a")
	if _, present := plain["records"]; present {
		t.Fatalf("records were returned to a caller that did not ask: %v", plain)
	}
	asked := requestJSON(t, handler, "/api/objects/qg-a?records=50")
	records, ok := asked["records"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("records = %v, want the object's own", asked["records"])
	}
	// The store's counters travel with the records so an empty list can be told
	// apart from an unrecorded one.
	if _, present := asked["diagnostics"]; !present {
		t.Fatalf("diagnostic health did not travel with the records: %v", asked)
	}
}

// A deployment with no store must say so rather than answer with an empty list,
// which reads as "nothing happened" and is a different next step.
func TestObjectDetailSaysWhenRecordsAreNotWired(t *testing.T) {
	handler := diagnosticsTestHandler(t, nil)
	body := requestJSON(t, handler, "/api/objects/qg-a?records=50")
	if body["records_error"] == nil {
		t.Fatalf("an unwired store answered as if it had no records: %v", body)
	}
}

func diagnosticsTestHandler(t *testing.T, store *DiagnosticStore) http.Handler {
	t.Helper()
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = append(snapshots[1].Anomalies, anomaly("qg-a"))
	snapshots[1].TotalAnomalies = 1
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}},
		stubRegistry{replicas: []string{"pod-a", "pod-b"}}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func requestJSON(t *testing.T, handler http.Handler, target string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, recorder.Body.String())
	}
	return body
}
