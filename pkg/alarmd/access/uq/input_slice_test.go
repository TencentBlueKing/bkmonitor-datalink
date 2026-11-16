// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// orGroupsConfig is a metric strategy grouped by host whose conditions are
// two OR groups, A=1 or B=2, as the platform stores it.
const orGroupsConfig = `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary",` +
	`"metric_field":"usage","agg_method":"AVG","agg_interval":60,"alias":"a","agg_dimension":["host"],` +
	`"agg_condition":[{"key":"A","method":"eq","value":["1"]},{"condition":"or","key":"B","method":"eq","value":["2"]}]}`

// partitionSlices are three slices of host that partition every series: a
// value ending in "a", a value ending in anything else, and an empty or
// missing value.
var partitionSlices = []execution.InputSlice{
	{Conditions: []execution.InputSliceCondition{{Dimension: "host", Pattern: "^(?s:.*)a$"}}},
	{Conditions: []execution.InputSliceCondition{{Dimension: "host", Pattern: "^(?s:.*)[^a]$"}}},
	{Conditions: []execution.InputSliceCondition{{Dimension: "host", Negated: true, Pattern: "^(?s:.+)$"}}},
}

// recordingServer answers every query with no series and keeps each body it
// was sent.
type recordingServer struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (server *recordingServer) start(t *testing.T) *Client {
	t.Helper()
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		server.mu.Lock()
		server.bodies = append(server.bodies, body)
		server.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"trace","is_partial":false,"result_table_id":["system.cpu_summary"]}`))
	}))
	t.Cleanup(httpServer.Close)
	client, err := NewClient(httpServer.URL, "alarmd", httpServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func (server *recordingServer) sent() [][]byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([][]byte(nil), server.bodies...)
}

// slicedAttempt is an evaluation attempt of spec reading slice.
func slicedAttempt(t *testing.T, spec execution.PhysicalQuerySpec, slice execution.InputSlice) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	attempt.Spec, attempt.Slice = spec, slice
	return attempt
}

// wireConditions is the conditions of each query clause of a sent body.
func wireConditions(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var decoded struct {
		QueryList []struct {
			Conditions json.RawMessage `json:"conditions"`
		} `json:"query_list"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	result := make([]json.RawMessage, 0, len(decoded.QueryList))
	for _, clause := range decoded.QueryList {
		result = append(result, clause.Conditions)
	}
	return result
}

// withoutConditions is a sent body with every clause's conditions taken out,
// what a slice must leave as it was.
func withoutConditions(t *testing.T, body []byte) any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, clause := range decoded["query_list"].([]any) {
		delete(clause.(map[string]any), "conditions")
	}
	return decoded
}

// A sliced query sends the slice's conditions in both OR groups of A=1 or
// B=2, each after the group's own, and nothing else of the request changes.
// In the last group only, a series with A=1 would be read by every slice.
func TestASlicedQueryPutsTheSliceIntoEveryOrGroup(t *testing.T) {
	spec := compiledSpec(t, orGroupsConfig)
	frozen := spec
	server := &recordingServer{}
	client := server.start(t)
	slice := execution.InputSlice{Conditions: []execution.InputSliceCondition{
		{Dimension: "host", Pattern: "^(?s:.*)a$"}, {Dimension: "host", Negated: true, Pattern: "^(?s:.*)ba$"}}}
	if _, err := client.Execute(context.Background(), slicedAttempt(t, spec, execution.InputSlice{}), &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(context.Background(), slicedAttempt(t, spec, slice), &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	bodies := server.sent()
	if len(bodies) != 2 {
		t.Fatalf("sent %d requests, want the whole query and its slice", len(bodies))
	}
	var unsliced struct {
		Fields     []json.RawMessage `json:"field_list"`
		Connectors []string          `json:"condition_list"`
	}
	if err := json.Unmarshal(wireConditions(t, bodies[0])[0], &unsliced); err != nil {
		t.Fatal(err)
	}
	if len(unsliced.Fields) != 2 || !reflect.DeepEqual(unsliced.Connectors, []string{"or"}) {
		t.Fatalf("setup: the unsliced conditions are %d fields joined by %v, want A=1 or B=2", len(unsliced.Fields), unsliced.Connectors)
	}
	in := json.RawMessage(`{"field_name":"host","op":"req","value":["^(?s:.*)a$"]}`)
	notIn := json.RawMessage(`{"field_name":"host","op":"nreq","value":["^(?s:.*)ba$"]}`)
	want, _ := json.Marshal(map[string]any{
		"field_list":     []json.RawMessage{unsliced.Fields[0], in, notIn, unsliced.Fields[1], in, notIn},
		"condition_list": []string{"and", "and", "or", "and", "and"},
	})
	if got := wireConditions(t, bodies[1])[0]; !jsonEqual(got, want) {
		t.Fatalf("sliced conditions = %s\nwant %s", got, want)
	}
	if !reflect.DeepEqual(withoutConditions(t, bodies[0]), withoutConditions(t, bodies[1])) {
		t.Fatalf("the slice changed the request beyond its conditions:\n%s\n%s", bodies[0], bodies[1])
	}
	if !reflect.DeepEqual(spec, frozen) {
		t.Fatal("the sliced read changed the frozen spec it was built from")
	}
	unchanged := spec
	unchanged.Digest = ""
	if rebuilt, err := execution.BuildPhysicalQuerySpec(unchanged); err != nil || rebuilt.Digest != frozen.Digest ||
		rebuilt.PlanFacts.QueryRevision != frozen.PlanFacts.QueryRevision {
		t.Fatalf("the spec's digest or revision moved: %+v, %v", rebuilt.Digest, err)
	}
}

// The zero slice sends the very bytes the query sent before slicing
// existed: the same bytes buildWireRequest gives, which the ordinary wire
// goldens pin.
func TestTheZeroSliceSendsTheUnslicedBytes(t *testing.T) {
	for _, spec := range []execution.PhysicalQuerySpec{compiledSpec(t, orGroupsConfig), validAttempt(t).Spec} {
		server := &recordingServer{}
		client := server.start(t)
		if _, err := client.Execute(context.Background(), slicedAttempt(t, spec, execution.InputSlice{}), &collectingSink{}); err != nil {
			t.Fatal(err)
		}
		_, want, err := buildWireRequest(spec)
		if err != nil {
			t.Fatal(err)
		}
		if bodies := server.sent(); len(bodies) != 1 || !bytes.Equal(bodies[0], want) {
			t.Fatalf("sent %q, want %s", bodies, want)
		}
	}
}

// A slice the query cannot be read in is refused by name before a request
// is sent.
func TestASliceTheQueryCannotBeReadInIsRefusedBeforeSending(t *testing.T) {
	inHost := execution.InputSlice{Conditions: []execution.InputSliceCondition{{Dimension: "host", Pattern: "^(?s:.*)a$"}}}
	for _, test := range []struct {
		name   string
		spec   func(*testing.T) execution.PhysicalQuerySpec
		slice  execution.InputSlice
		reason string
	}{
		{name: "promql", reason: "promql", slice: inHost, spec: func(t *testing.T) execution.PhysicalQuerySpec {
			return compiledSpec(t, `{"data_source_label":"prometheus","data_type_label":"time_series","promql":"sum by (host) (rate(x[1m]))","agg_interval":60,"alias":"a"}`)
		}},
		{name: "bkdata", reason: "storage_dialect", slice: inHost, spec: func(t *testing.T) execution.PhysicalQuerySpec {
			return rebuiltSpec(t, compiledSpec(t, orGroupsConfig), func(facts *execution.QueryPlanFacts) {
				facts.QueryList[0].DataSource = "bkdata"
				facts.SourceSemantics = []string{"bk_data/time_series"}
			})
		}},
		{name: "log_search", reason: "storage_dialect", slice: inHost, spec: func(t *testing.T) execution.PhysicalQuerySpec {
			return compiledSpec(t, `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":"71","query_string":"error","agg_interval":60,"alias":"a","agg_dimension":["host"]}`)
		}},
		{name: "not_grouped", reason: "dimension_not_grouped", spec: func(t *testing.T) execution.PhysicalQuerySpec {
			return compiledSpec(t, orGroupsConfig)
		}, slice: execution.InputSlice{Conditions: []execution.InputSliceCondition{{Dimension: "A", Pattern: "^(?s:.*)1$"}}}},
		{name: "aliased", reason: "dimension_not_grouped", slice: inHost, spec: func(t *testing.T) execution.PhysicalQuerySpec {
			return rebuiltSpec(t, compiledSpec(t, orGroupsConfig), func(facts *execution.QueryPlanFacts) {
				facts.Normalization.DimensionAliases = map[string]string{"host": "host_name"}
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &recordingServer{}
			client := server.start(t)
			_, err := client.Execute(context.Background(), slicedAttempt(t, test.spec(t), test.slice), &collectingSink{})
			if reason, ok := execution.InputSliceUnsupportedOf(err); !ok || reason != test.reason {
				t.Fatalf("Execute() error = %v, want the slice refused as %s", err, test.reason)
			}
			if bodies := server.sent(); len(bodies) != 0 {
				t.Fatalf("sent %d requests before refusing the slice, want none", len(bodies))
			}
		})
	}
}

// rebuiltSpec is spec with its facts changed by mutate, its revision and
// digest derived again.
func rebuiltSpec(t *testing.T, spec execution.PhysicalQuerySpec, mutate func(*execution.QueryPlanFacts)) execution.PhysicalQuerySpec {
	t.Helper()
	facts := spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	mutate(&facts)
	rebuiltFacts, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	spec.PlanFacts, spec.Digest = rebuiltFacts, ""
	rebuilt, err := execution.BuildPhysicalQuerySpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	return rebuilt
}

// The conditions a partition of A=1 or B=2 sends, recorded in testdata so
// that the provider's own condition evaluation can read them: unify-query's
// TestAlarmdInputSlicesPartitionTheUnslicedSeries evaluates this file with
// the provider's local filter and with its Prometheus matchers. The file is
// written by hand from the slices and the strategy's two groups, not from
// this client's output; a change here is a change the provider-side check
// has to see.
func TestTheSlicePartitionConditionsAreTheRecordedOnes(t *testing.T) {
	spec := compiledSpec(t, orGroupsConfig)
	recorded := struct {
		Unsliced json.RawMessage   `json:"unsliced"`
		Slices   []json.RawMessage `json:"slices"`
	}{}
	_, unsliced, err := buildWireRequest(spec)
	if err != nil {
		t.Fatal(err)
	}
	recorded.Unsliced = wireConditions(t, unsliced)[0]
	for _, slice := range partitionSlices {
		_, body, err := buildWireRequestSliced(spec, false, slice)
		if err != nil {
			t.Fatal(err)
		}
		recorded.Slices = append(recorded.Slices, wireConditions(t, body)[0])
	}
	got, _ := json.Marshal(recorded)
	want, err := os.ReadFile(filepath.Join("testdata", "input_slice_partition.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, want) {
		t.Fatalf("the partition's conditions are\n%s\nrecorded\n%s", got, want)
	}
}

// A recheck of a slice's read reads the same slice: the request it sends is
// the sliced first read's byte for byte, and without a slice on ctx it is
// the whole Query Group's.
func TestARecheckOfASliceReadsTheSameSlice(t *testing.T) {
	spec := compiledSpec(t, orGroupsConfig)
	server := &recordingServer{}
	client := server.start(t)
	slice := execution.InputSlice{Conditions: []execution.InputSliceCondition{
		{Dimension: "host", Pattern: "^(?s:.*)a$"}, {Dimension: "host", Negated: true, Pattern: "^(?s:.*)ba$"}}}
	if _, err := client.Execute(context.Background(), slicedAttempt(t, spec, slice), &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	if _, err := client.Recheck(execution.WithInputSlice(ctx, slice), spec, &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Recheck(ctx, spec, &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	bodies := server.sent()
	if len(bodies) != 3 {
		t.Fatalf("sent %d requests, want the sliced read and two rechecks", len(bodies))
	}
	if !jsonEqual(wireConditions(t, bodies[1])[0], wireConditions(t, bodies[0])[0]) {
		t.Fatalf("the recheck of the slice asked for other conditions:\n%s\n%s", bodies[0], bodies[1])
	}
	if jsonEqual(wireConditions(t, bodies[2])[0], wireConditions(t, bodies[0])[0]) {
		t.Fatal("a recheck without a slice asked for the slice")
	}
}
