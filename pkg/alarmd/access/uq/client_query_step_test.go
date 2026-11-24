// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// steppedAttempt is the query of a Plan detected every fifteen seconds over
// one-minute windows: one minute's window starting 35 s off the minute grid,
// asked fifteen seconds apart over the part of it whose buckets lie inside.
func steppedAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	attempt := unalignedAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision, facts.QueryStepMillis = "", 15_000
	rebuilt, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	window := attempt.Spec.LogicalWindow
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: rebuilt,
		LogicalWindow: window, ProviderRange: rebuilt.RequestRange(window), AcceptedRange: rebuilt.RequestRange(window),
		RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec = spec
	return attempt
}

// recordingClient answers every request with body and keeps what it was asked.
func recordingClient(t *testing.T, body string) (*Client, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var asked map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		mu.Lock()
		_ = json.Unmarshal(raw, &asked)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClientWithLimits(server.URL, "alarmd-shadow", server.Client(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return client, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return asked
	}
}

// The request asks for the step, over one step: the bucket that starts at
// the window's start. Buckets the query service still answers past it would
// read data after the window's end, and are not used; the one used is the
// window, labelled at its start.
func TestASteppedQueryIsAskedTheStepOverOneStepAndUsesItsFirstBucket(t *testing.T) {
	attempt := steppedAttempt(t)
	client, asked := recordingClient(t, pointsBody(1_700_123_015, 1_700_123_030, 1_700_123_045, 1_700_123_060))
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), attempt, sink)
	if err != nil || completion.Completeness != execution.CompletenessFull || len(sink.batches) != 1 || sink.batches[0].Dataset.Len() != 1 {
		t.Fatalf("completion %+v (%v) with %d batches, want one bucket used", completion, err, len(sink.batches))
	}
	if record, found := sink.batches[0].Dataset.Record(0); !found || record.SourceTime() != 1_700_123_015 {
		t.Fatalf("used %+v, want the bucket at the window's start", record)
	}
	body := asked()
	if body["step"] != "15s" || body["start_time"] != "1700123015" || body["end_time"] != "1700123030" || body["not_time_align"] != true {
		t.Fatalf("asked step=%v start=%v end=%v not_time_align=%v, want 15s over [1700123015,1700123030) unaligned",
			body["step"], body["start_time"], body["end_time"], body["not_time_align"])
	}
}

// A bucket between steps is on no grid the request asked for: the storage
// bucketed on its own, and nothing of the answer is used.
func TestASteppedQueryAnsweredBetweenItsStepsIsNotUsed(t *testing.T) {
	client, _ := recordingClient(t, pointsBody(1_700_123_020))
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), steppedAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessUnavailable || len(sink.batches) != 0 ||
		completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonDetectIntervalStorageNotSliding) {
		t.Fatalf("completion %+v with %d batches, want unavailable as %s", completion, len(sink.batches), contract.ReasonDetectIntervalStorageNotSliding)
	}
}
