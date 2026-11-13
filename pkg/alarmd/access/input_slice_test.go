// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// hostEndingInA is a slice of the series whose host ends in "a".
var hostEndingInA = execution.InputSlice{Conditions: []execution.InputSliceCondition{{Dimension: "host", Pattern: "^(?s:.*)a$"}}}

// slicedConditions is what a clause of A=1 or B=2 sends read in
// hostEndingInA, written out by hand: the slice's condition after each
// group's own.
const slicedConditions = `{"field_list":[{"field_name":"A","op":"eq","value":["1"]},` +
	`{"field_name":"host","op":"req","value":["^(?s:.*)a$"]},{"field_name":"B","op":"eq","value":["2"]},` +
	`{"field_name":"host","op":"req","value":["^(?s:.*)a$"]}],"condition_list":["and","or","and"]}`

// unslicedConditions is what the same clause sends unsliced.
const unslicedConditions = `{"field_list":[{"field_name":"A","op":"eq","value":["1"]},` +
	`{"field_name":"B","op":"eq","value":["2"]}],"condition_list":["or"]}`

// orGroupsExecution is the frozen Slot of a metric strategy grouped by host
// whose conditions are A=1 or B=2, with a year-on-year dependency beside its
// primary query; dependency changes the dependency's facts.
func orGroupsExecution(t *testing.T, dependency func(*execution.QueryPlanFacts)) (execution.FrozenExecutionContractRef, FrozenPlan) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	facts := rekeyed(t, frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef], func(facts *execution.QueryPlanFacts) {
		facts.QueryList[0].Dimensions = []string{"host"}
		facts.QueryList[0].Conditions = execution.QueryConditions{Fields: []execution.QueryConditionField{
			{Field: "A", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "1"}}},
			{Field: "B", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "2"}}},
		}, Connectors: []string{"or"}}
	})
	ref := execution.LogicalQueryRef(facts.QueryRevision)
	frozen.QueryFacts = map[execution.LogicalQueryRef]execution.QueryPlanFacts{ref: facts}
	frozen.Requirements[0].LogicalQueryRef = ref
	contractRef.QueryRevision = facts.QueryRevision
	withYesterdayDependency(t, &frozen)
	if dependency != nil {
		requirement := &frozen.Requirements[1]
		changed := rekeyed(t, frozen.QueryFacts[requirement.LogicalQueryRef], dependency)
		delete(frozen.QueryFacts, requirement.LogicalQueryRef)
		requirement.LogicalQueryRef = execution.LogicalQueryRef(changed.QueryRevision)
		frozen.QueryFacts[requirement.LogicalQueryRef] = changed
	}
	return bindFrozenDueDigest(t, contractRef, frozen), frozen
}

// rekeyed is facts changed by mutate, with their revision derived again.
func rekeyed(t *testing.T, facts execution.QueryPlanFacts, mutate func(*execution.QueryPlanFacts)) execution.QueryPlanFacts {
	t.Helper()
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	mutate(&facts)
	built, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// uqRecorder is the query service: it answers every query with no series
// and keeps each body it was sent.
type uqRecorder struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (recorder *uqRecorder) sent() [][]byte {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([][]byte(nil), recorder.bodies...)
}

// liveDeadline is the query service client with each attempt's deadline
// moved to a minute from now: the fixture's Slot is years past, and the
// client bounds its request by the attempt's deadline on the real clock.
// Nothing else of the attempt is touched, the slice least of all.
type liveDeadline struct{ client *uq.Client }

func (provider liveDeadline) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	attempt.DeadlineUnixMilli = time.Now().Add(time.Minute).UnixMilli()
	return provider.client.Execute(ctx, attempt, sink)
}

// slicedSource is a Source over frozen whose provider is the real query
// service client talking to recorder.
func slicedSource(t *testing.T, contractRef execution.FrozenExecutionContractRef, frozen FrozenPlan, recorder *uqRecorder) *Source {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, body)
		recorder.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"trace","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(server.Close)
	client, err := uq.NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, liveDeadline{client: client}, &recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	slotEnd := int64(contractRef.Slot.EvaluationTime)
	source.now = func() time.Time { return time.Unix(slotEnd, 0) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	return source
}

// conditionsOf is the conditions of each clause of a sent body.
func conditionsOf(t *testing.T, body []byte) []string {
	t.Helper()
	var decoded struct {
		QueryList []struct {
			Conditions json.RawMessage `json:"conditions"`
		} `json:"query_list"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(decoded.QueryList))
	for _, clause := range decoded.QueryList {
		result = append(result, string(clause.Conditions))
	}
	return result
}

func sameJSON(t *testing.T, left, right string) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// A Slot read in a slice sends the slice's conditions in every OR group of
// its primary query and of its dependency alike, so a series lands in the
// same slice for both and nothing joins them afterwards. The zero slice
// sends the conditions as they were. The frozen facts the queries are built
// from are not touched.
func TestASlicedSlotSendsTheSliceInEveryGroupOfThePrimaryAndTheDependency(t *testing.T) {
	for _, test := range []struct {
		name  string
		slice execution.InputSlice
		want  string
	}{
		{name: "sliced", slice: hostEndingInA, want: slicedConditions},
		{name: "zero", want: unslicedConditions},
	} {
		t.Run(test.name, func(t *testing.T) {
			contractRef, frozen := orGroupsExecution(t, nil)
			before := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts, len(frozen.QueryFacts))
			for ref, facts := range frozen.QueryFacts {
				before[ref] = facts
			}
			recorder := &uqRecorder{}
			source := slicedSource(t, contractRef, frozen, recorder)
			completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
				Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1, Slice: test.slice}, &recordingConsumer{})
			if err != nil {
				t.Fatal(err)
			}
			bodies := recorder.sent()
			if len(bodies) != 2 {
				t.Fatalf("sent %d queries, want the primary and the dependency", len(bodies))
			}
			for _, body := range bodies {
				if got := conditionsOf(t, body); len(got) != 1 || !sameJSON(t, got[0], test.want) {
					t.Fatalf("a query sent conditions %v, want %s", got, test.want)
				}
			}
			if !reflect.DeepEqual(frozen.QueryFacts, before) {
				t.Fatal("the execution changed the frozen query facts")
			}
			for _, query := range completion.PhysicalQueries {
				if _, found := before[execution.LogicalQueryRef(query.QueryRevision)]; !found {
					t.Fatalf("a completion names revision %s, which no frozen query has", query.QueryRevision)
				}
			}
		})
	}
}

// A Slot whose queries cannot all be read in the slice is refused by name
// before any query is sent and before the consumer is begun: the dependency
// not grouped by the dimension, or read from a store whose regular
// expressions the slices cannot rest on, or a supplement.
func TestASlotThatCannotBeReadInTheSliceIsRefusedBeforeAnyQuery(t *testing.T) {
	for _, test := range []struct {
		name       string
		operation  execution.Operation
		dependency func(*execution.QueryPlanFacts)
		reason     string
	}{
		{name: "dependency_not_grouped", operation: execution.OperationNormal, reason: "dimension_not_grouped",
			dependency: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].Dimensions = []string{"region"} }},
		{name: "dependency_bkdata", operation: execution.OperationNormal, reason: "storage_dialect",
			dependency: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].DataSource = "bkdata" }},
		{name: "supplement", operation: execution.OperationSupplement, reason: "supplement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			contractRef, frozen := orGroupsExecution(t, test.dependency)
			recorder := &uqRecorder{}
			source := slicedSource(t, contractRef, frozen, recorder)
			consumer := &recordingConsumer{}
			_, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
				Contract: contractRef, Operation: test.operation, AttemptNo: 1, Slice: hostEndingInA}, consumer)
			if reason, ok := execution.InputSliceUnsupportedOf(err); !ok || reason != test.reason {
				t.Fatalf("Execute() error = %v, want the slice refused as %s", err, test.reason)
			}
			if bodies := recorder.sent(); len(bodies) != 0 || consumer.begin != 0 {
				t.Fatalf("sent %d queries and began the consumer %d times before refusing, want neither", len(bodies), consumer.begin)
			}
		})
	}
}
