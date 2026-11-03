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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type overviewTracingRead struct {
	Tracing OverviewTracing `json:"tracing"`
	Runtime struct {
		Replicas []struct {
			Name    string        `json:"name"`
			Tracing *TracingFacts `json:"tracing"`
		} `json:"replicas"`
	} `json:"runtime"`
}

func readOverviewTracing(t *testing.T, snapshots []Snapshot) (overviewTracingRead, string) {
	t.Helper()
	handler := handlerWith(t, snapshots, Expectation{}, []string{"pod-a", "pod-b"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var read overviewTracingRead
	if err := json.Unmarshal(recorder.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	return read, recorder.Body.String()
}

func onFacts(host string) *TracingFacts {
	ratio, rate := 0.1, 5
	return &TracingFacts{State: TracingOn, EndpointHost: host, SampleRatio: &ratio, MaxTracesPerSecond: &rate}
}

func offFacts() *TracingFacts {
	return &TracingFacts{State: TracingOffNotConfigured, Reason: TracingReasonNotConfigured}
}

// The overview says in one read whether the replicas export their spans: on
// with the collector's host and sampling, not configured with why, mixed
// while they disagree, unknown when none said. Each replica row carries its
// own. Counters that stay at zero cannot tell off from idle; this can.
func TestTheOverviewSaysWhetherTheReplicasExportTheirSpans(t *testing.T) {
	on := idleSnapshots()
	on[0].Tracing, on[1].Tracing = onFacts("collector:4318"), onFacts("collector:4318")
	read, _ := readOverviewTracing(t, on)
	if read.Tracing.State != TracingOn || read.Tracing.Groups != 1 || read.Tracing.Facts == nil ||
		read.Tracing.Facts.EndpointHost != "collector:4318" || *read.Tracing.Facts.SampleRatio != 0.1 ||
		*read.Tracing.Facts.MaxTracesPerSecond != 5 || !strings.Contains(read.Tracing.Line, "collector:4318") {
		t.Fatalf("both on = %+v, want on at collector:4318, 0.1 and 5", read.Tracing)
	}
	for _, row := range read.Runtime.Replicas {
		if row.Tracing == nil || row.Tracing.State != TracingOn || row.Tracing.EndpointHost != "collector:4318" {
			t.Fatalf("row %s tracing = %+v, want its own on facts", row.Name, row.Tracing)
		}
	}

	off := idleSnapshots()
	off[0].Tracing, off[1].Tracing = offFacts(), offFacts()
	read, _ = readOverviewTracing(t, off)
	if read.Tracing.State != TracingOffNotConfigured || read.Tracing.Facts == nil ||
		read.Tracing.Facts.Reason != TracingReasonNotConfigured || read.Tracing.Facts.EndpointHost != "" ||
		read.Tracing.Facts.SampleRatio != nil || !strings.Contains(read.Tracing.Line, "未配置") {
		t.Fatalf("both off = %+v, want not configured with the reason and no host or sampling", read.Tracing)
	}

	mixed := idleSnapshots()
	mixed[0].Tracing, mixed[1].Tracing = onFacts("collector:4318"), offFacts()
	if read, _ = readOverviewTracing(t, mixed); read.Tracing.State != OverviewTracingMixed || read.Tracing.Groups != 2 || read.Tracing.Facts != nil {
		t.Fatalf("one on, one off = %+v, want mixed over two groups and no single facts", read.Tracing)
	}
	// A replica on an older build said nothing: never folded into the other's state.
	older := idleSnapshots()
	older[0].Tracing = onFacts("collector:4318")
	if read, _ = readOverviewTracing(t, older); read.Tracing.State != OverviewTracingMixed {
		t.Fatalf("one on, one silent = %+v, want mixed", read.Tracing)
	}
	if read, _ = readOverviewTracing(t, idleSnapshots()); read.Tracing.State != OverviewTracingUnknown || read.Tracing.Facts != nil {
		t.Fatalf("none said = %+v, want unknown", read.Tracing)
	}

	// The row is a copy: a later change to the snapshot's facts does not reach it.
	snapshots := idleSnapshots()
	snapshots[0].Tracing, snapshots[1].Tracing = onFacts("collector:4318"), onFacts("collector:4318")
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	*snapshots[0].Tracing.SampleRatio = 0.9
	for _, row := range view.PerReplica {
		if *row.Tracing.SampleRatio != 0.1 {
			t.Fatalf("row %s ratio %v: the row aliases the snapshot's facts", row.Replica, *row.Tracing.SampleRatio)
		}
	}
	if len(view.Tracings) != 1 || *view.Tracings[0].Tracing.SampleRatio != 0.1 {
		t.Fatalf("groups %+v alias the snapshot's facts", view.Tracings)
	}
}

// A ratio of 0 is on and samples nothing: it is said, not dropped as empty.
func TestAZeroSampleRatioIsSaidNotDropped(t *testing.T) {
	zero := idleSnapshots()
	facts := onFacts("collector:4318")
	*facts.SampleRatio = 0
	zero[0].Tracing, zero[1].Tracing = facts, CopyTracingFacts(facts)
	read, body := readOverviewTracing(t, zero)
	if read.Tracing.Facts == nil || read.Tracing.Facts.SampleRatio == nil || *read.Tracing.Facts.SampleRatio != 0 {
		t.Fatalf("ratio 0 = %+v (%s), want it said as 0", read.Tracing, body)
	}
}

// The public surface names no place: the collector's host stays off it, the
// groups an empty list rather than the full answer's.
func TestThePublicSurfaceCarriesNoSpanExport(t *testing.T) {
	full := HealthResponse{Tracings: []TracingGroup{{Tracing: onFacts("collector:4318"), Replicas: []string{"pod-a"}}}}
	public := PublicHealth(full)
	if public.Tracings == nil || len(public.Tracings) != 0 {
		t.Fatalf("public tracings = %+v, want an empty list", public.Tracings)
	}
	body, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "collector:4318") {
		t.Fatalf("the public surface names the collector: %s", body)
	}
}
