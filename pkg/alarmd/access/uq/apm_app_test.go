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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const apmConfig = `{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"bk_apm_count","result_table_id":"2_bkapm_metric_cross_bank_trade.__default__","agg_method":"SUM","agg_interval":60,"agg_dimension":["service_name"],"agg_condition":[],"alias":"a"}`

// An APM query asks the provider for app_name beside the strategy's own
// dimensions -- in the clause, the columns it keeps and the aggregation --
// and nothing about the query alarmd froze changes: the spec, its digest and
// its revision are the ones the request was built from.
func TestAnAPMQueryAsksForItsApplicationWithoutChangingTheFrozenQuery(t *testing.T) {
	spec := compiledSpec(t, apmConfig)
	before, _ := json.Marshal(spec)
	_, wire, err := buildWireRequest(spec)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		QueryList []struct {
			Dimensions  []string `json:"dimensions"`
			KeepColumns []string `json:"keep_columns"`
			Functions   []struct {
				Dimensions []string `json:"dimensions"`
			} `json:"function"`
		} `json:"query_list"`
	}
	if err := json.Unmarshal(wire, &body); err != nil || len(body.QueryList) != 1 {
		t.Fatalf("wire %s: %v", wire, err)
	}
	clause := body.QueryList[0]
	if !reflect.DeepEqual(clause.Dimensions, []string{"service_name", "app_name"}) || len(clause.Functions) == 0 ||
		!reflect.DeepEqual(clause.Functions[0].Dimensions, []string{"service_name", "app_name"}) ||
		(len(clause.KeepColumns) > 0 && !strings.Contains(strings.Join(clause.KeepColumns, ","), "app_name")) {
		t.Fatalf("wire %s, want app_name in the clause, its aggregation and its columns", wire)
	}
	after, _ := json.Marshal(spec)
	digest, err := execution.DerivePhysicalQueryDigest(spec)
	if string(before) != string(after) || err != nil || digest != spec.Digest {
		t.Fatalf("building the request changed the frozen query: digest %s vs %s (%v)", digest, spec.Digest, err)
	}
	// Without the carry the request is the one the strategy alone asks for.
	_, plain, err := buildWireRequestCarrying(spec, false)
	if err != nil || strings.Contains(string(plain), "app_name") {
		t.Fatalf("uncarried wire %s (%v)", plain, err)
	}
}

// The application is carried only when every clause reads the same APM
// table: never for another table, nor for clauses on different tables,
// whose join on group keys an extra key would change.
func TestTheApplicationIsCarriedOnlyForOneAPMTable(t *testing.T) {
	spec := compiledSpec(t, apmConfig)
	if !carriesAPMApp(spec) {
		t.Fatal("an APM metric table is not carried")
	}
	other := spec
	other.PlanFacts.QueryList = append([]execution.QueryClause(nil), spec.PlanFacts.QueryList...)
	other.PlanFacts.QueryList[0].TableID = "2_system.cpu_summary"
	if carriesAPMApp(other) {
		t.Fatal("a non-APM table is carried")
	}
	mixed := spec
	second := spec.PlanFacts.QueryList[0]
	second.TableID, second.ReferenceName = "2_bkapm_metric_cross_bank_counter.__default__", "b"
	mixed.PlanFacts.QueryList = append(append([]execution.QueryClause(nil), spec.PlanFacts.QueryList...), second)
	if carriesAPMApp(mixed) {
		t.Fatal("clauses on two APM tables are carried")
	}
	promql := spec
	promql.PlanFacts.PromQL = &execution.PromQLQuery{Expression: "up"}
	if carriesAPMApp(promql) {
		t.Fatal("a PromQL query is carried")
	}
}

// The series identity, the record ids and so every state key and alert
// fingerprint built from them are the same whether the provider's series
// carries app_name or not; the record carries it beside them.
func TestACarriedApplicationLeavesTheSeriesIdentityAsItWas(t *testing.T) {
	spec := compiledSpec(t, apmConfig)
	plain, _, err := normalizeSeries(spec, "provider-result", identityTestSeries([]string{"service_name"}, []string{"transfer"}), 1_700_123_500)
	if err != nil {
		t.Fatal(err)
	}
	carried, _, err := normalizeSeries(spec, "provider-result", identityTestSeries([]string{"service_name", "app_name"}, []string{"transfer", "cross-bank-trade"}), 1_700_123_500)
	if err != nil {
		t.Fatal(err)
	}
	a, b := plain.Dataset.Records(), carried.Dataset.Records()
	if len(a) != 1 || len(b) != 1 || a[0].RecordID != b[0].RecordID || !reflect.DeepEqual(a[0].DimensionIdentity, b[0].DimensionIdentity) {
		t.Fatalf("identity differs: %+v vs %+v", a, b)
	}
	if string(b[0].Dimensions["app_name"]) != `"cross-bank-trade"` {
		t.Fatalf("carried record dimensions %v, want app_name as written", b[0].Dimensions)
	}
}

// apmServer answers an APM query: carried when the request groups by
// app_name, plain when it does not, and counts each kind.
func apmServer(t *testing.T, carried, plain string) (*httptest.Server, *[]bool) {
	t.Helper()
	var asked []bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		withApp := strings.Contains(string(body), `"app_name"`)
		asked = append(asked, withApp)
		writer.Header().Set("Content-Type", "application/json")
		answer := plain
		if withApp {
			answer = carried
		}
		_, _ = writer.Write([]byte(`{"series":[` + answer + `],"status":null,"trace_id":"t","is_partial":false,"result_table_id":["2_bkapm_metric_cross_bank_trade"]}`))
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

func apmSeries(app string, value string) string {
	keys, values := `"service_name"`, `"transfer"`
	if app != "" {
		keys, values = keys+`,"app_name"`, values+`,"`+app+`"`
	}
	return `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[` + keys + `],"group_values":[` + values + `],"values":[[1700123460000,` + value + `]]}`
}

func apmAttempt(t *testing.T) execution.QueryAttempt {
	return execution.QueryAttempt{Spec: compiledSpec(t, apmConfig), Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1_700_124_000},
		Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
}

// A carried response is handed on whole once every identity came back once:
// one request, its series carrying the application, and the response's
// size reported.
func TestACarriedResponseIsHandedOnWhenNothingSplit(t *testing.T) {
	server, asked := apmServer(t, apmSeries("cross-bank-trade", "7"), apmSeries("", "7"))
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var heard []APMCarriedResponse
	client.WithAPMObserver(func(response APMCarriedResponse) { heard = append(heard, response) })
	sink := &collectingSink{}
	if _, err := client.Execute(context.Background(), apmAttempt(t), sink); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || !(*asked)[0] || len(sink.batches) != 1 {
		t.Fatalf("asked %v, batches %d, want one carried request handed on", *asked, len(sink.batches))
	}
	if record := sink.batches[0].Dataset.Records()[0]; string(record.Dimensions["app_name"]) != `"cross-bank-trade"` {
		t.Fatalf("record dimensions %v", record.Dimensions)
	}
	if len(heard) != 1 || heard[0].NotUnique || heard[0].Series != 1 || heard[0].Bytes == 0 || heard[0].Table != APMTableMetric {
		t.Fatalf("heard %+v", heard)
	}
}

// An identity that comes back twice is a table holding two applications, and
// the extra key has split one series into parts. Nothing of that response is
// handed on: it is said, and the query read again without the application,
// so detection sees the strategy's own series and no application is named.
func TestASplitResponseIsReadAgainWithoutTheApplication(t *testing.T) {
	server, asked := apmServer(t, apmSeries("cross-bank-trade", "3")+","+apmSeries("cross-bank-trade-old", "4"), apmSeries("", "7"))
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var heard []APMCarriedResponse
	client.WithAPMObserver(func(response APMCarriedResponse) { heard = append(heard, response) })
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), apmAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 2 || !(*asked)[0] || (*asked)[1] {
		t.Fatalf("asked %v, want a carried read then one without", *asked)
	}
	if len(sink.batches) != 1 {
		t.Fatalf("handed on %d batches, want only the plain read's", len(sink.batches))
	}
	record := sink.batches[0].Dataset.Records()[0]
	if _, carried := record.Dimensions["app_name"]; carried || string(record.Values["value"]) != "7" {
		t.Fatalf("record %v %v, want the plain series' value and no application", record.Dimensions, record.Values)
	}
	if completion.Delivery.Series != 1 || len(heard) != 1 || !heard[0].NotUnique || heard[0].Series != 2 {
		t.Fatalf("completion %+v heard %+v", completion.Delivery, heard)
	}
}
