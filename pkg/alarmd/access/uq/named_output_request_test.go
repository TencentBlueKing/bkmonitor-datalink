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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A query with named outputs asks for them on both paths: the structured
// request keeps metric_merge and names each other output by its query_list
// reference, the PromQL request keeps the compiled PromQL on top and names
// each other output by its full PromQL.
func TestAQueryWithNamedOutputsAsksForThem(t *testing.T) {
	for _, test := range []struct {
		name, path, wire string
		spec             execution.PhysicalQuerySpec
	}{
		{name: "structured", path: "/query/ts",
			spec: compiledNamedSpec(t, "a + b", namedOutputsAB, namedTimeSeriesA, namedTimeSeriesB),
			wire: `"metric_merge":"a + b",` + `"start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":false,` +
				`"response_contract":"named_outputs/v1","legacy_output_ref":"C","output_list":[{"reference_name":"A","expression":"a"},{"reference_name":"B","expression":"b"},{"reference_name":"C","expression":"a + b"}]}`},
		{name: "promql", path: "/query/ts/promql",
			spec: compiledNamedSpec(t, "a + b", namedOutputsAB, namedPromQLA, namedPromQLB),
			wire: `{"end":"1700124000","legacy_output_ref":"C","match":"","output_list":[{"reference_name":"A","expression":"sum by (job) (up)"},{"reference_name":"B","expression":"sum by (job) (down)"},{"reference_name":"C","expression":"(sum by (job) (up)) + (sum by (job) (down))"}],"promql":"(sum by (job) (up)) + (sum by (job) (down))","response_contract":"named_outputs/v1","start":"1700123000","step":"60s","timezone":"Asia/Shanghai"}`},
	} {
		path, wire, err := buildWireRequest(test.spec)
		if err != nil {
			t.Fatal(err)
		}
		if path != test.path || len(wire) < len(test.wire) || string(wire[len(wire)-len(test.wire):]) != test.wire {
			t.Errorf("%s: %s\n%s\nwant it to end\n%s", test.name, path, wire, test.wire)
		}
	}
}

// Without named outputs a PromQL query is the request it was, byte for
// byte: the body below was recorded from the build before named outputs
// were asked for. The structured requests are pinned the same way by
// TestOrdinaryQueriesAreUnchangedByteForByte.
func TestAPromQLQueryWithoutNamedOutputsIsUnchangedByteForByte(t *testing.T) {
	spec := compiledNamedSpec(t, "a + b", "", namedPromQLA, namedPromQLB)
	path, wire, err := buildWireRequest(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"end":"1700124000","match":"","promql":"(sum by (job) (up)) + (sum by (job) (down))","start":"1700123000","step":"60s","timezone":"Asia/Shanghai"}`
	if path != "/query/ts/promql" || string(wire) != want {
		t.Fatalf("%s %s\nwant %s", path, wire, want)
	}
}

// A query asking for named outputs does not offer the shared schema: the
// named answer is its own JSON shape. The same query without them still
// offers it.
func TestAQueryWithNamedOutputsDoesNotNegotiateTheSharedSchema(t *testing.T) {
	var accepts []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		accepts = append(accepts, request.Header.Get("Accept"))
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"series":[],"is_partial":false}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, outputs := range []string{namedOutputsAB, ""} {
		spec := compiledNamedSpec(t, "a + b", outputs, namedTimeSeriesA, namedTimeSeriesB)
		attempt := execution.QueryAttempt{Spec: spec, Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1_700_124_000},
			Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
		if _, err := client.Execute(context.Background(), attempt, &collectingSink{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(accepts) != 2 || accepts[0] != "" || accepts[1] != sharedSchemaAccept {
		t.Fatalf("accept headers %q", accepts)
	}
}

// Asked and answered over the wire: the provider sees the outputs asked
// for, and the completion carries them aligned to the records.
func TestANamedOutputsQueryIsAskedAndAnsweredOverTheWire(t *testing.T) {
	var asked struct {
		ResponseContract string `json:"response_contract"`
		LegacyOutputRef  string `json:"legacy_output_ref"`
		OutputList       []struct {
			ReferenceName string `json:"reference_name"`
		} `json:"output_list"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&asked)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(namedAnswer(
			namedOutput("A", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1]]`)),
			namedOutput("B", "SUCCESS", false, ""),
			namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`)))))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), namedAttempt(t, namedOutputsAB), sink)
	if err != nil {
		t.Fatal(err)
	}
	if asked.ResponseContract != execution.NamedOutputsContract || asked.LegacyOutputRef != "C" || len(asked.OutputList) != 3 {
		t.Fatalf("asked %+v", asked)
	}
	values := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, values, "A", "1", execution.NamedOutputSuccess)
	wantOutput(t, values, "B", "", execution.NamedOutputMissing)
	wantOutput(t, values, "C", "11", execution.NamedOutputSuccess)
}
