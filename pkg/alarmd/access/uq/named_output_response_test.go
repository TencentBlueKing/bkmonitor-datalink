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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// compiledNamedSpec is a physical query compiled from a strategy item of
// several query configs, its expression and its query_output_config.
func compiledNamedSpec(t *testing.T, expression, outputConfig string, configs ...string) execution.PhysicalQuerySpec {
	t.Helper()
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq", "Asia/Shanghai", controlplane.LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]json.RawMessage, 0, len(configs))
	for _, config := range configs {
		raw = append(raw, json.RawMessage(config))
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "141", ItemID: "3", QueryMD5: "named", Expression: expression, QueryConfigs: raw,
		QueryOutputConfig: json.RawMessage(outputConfig)})
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window,
		ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

const (
	namedTimeSeriesA = `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu","metric_field":"usage","agg_method":"AVG","agg_interval":60,"alias":"a","agg_dimension":["bk_target_ip"]}`
	namedTimeSeriesB = `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu","metric_field":"idle","agg_method":"AVG","agg_interval":60,"alias":"b","agg_dimension":["bk_target_ip"]}`
	namedPromQLA     = `{"data_source_label":"prometheus","data_type_label":"time_series","expression_mode":"promql","alias":"a","promql":"sum by (job) (up)","agg_interval":60}`
	namedPromQLB     = `{"data_source_label":"prometheus","data_type_label":"time_series","expression_mode":"promql","alias":"b","promql":"sum by (job) (down)","agg_interval":60}`
	namedOutputsAB   = `{"response_contract":"named_outputs/v1","legacy_output_ref":"C","output_list":[{"reference_name":"A","expression":"a"},{"reference_name":"B","expression":"b"},{"reference_name":"C","expression":"a + b"}]}`
)

func namedAttempt(t *testing.T, outputConfig string) execution.QueryAttempt {
	t.Helper()
	return execution.QueryAttempt{Spec: compiledNamedSpec(t, "a + b", outputConfig, namedTimeSeriesA, namedTimeSeriesB),
		Slot:      execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1_700_124_000},
		Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
}

// namedSeries is one series of an answer: its group keys and values in
// order, and its points as [milliseconds, value] pairs.
func namedSeries(keys, values []string, points string) string {
	encodedKeys, _ := json.Marshal(keys)
	encodedValues, _ := json.Marshal(values)
	return `{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":` + string(encodedKeys) +
		`,"group_values":` + string(encodedValues) + `,"values":` + points + `}`
}

func namedOutput(ref, state string, partial bool, status string, series ...string) string {
	partialText := "false"
	if partial {
		partialText = "true"
	}
	statusText := ""
	if status != "" {
		statusText = `"status":{"code":"` + status + `","message":"output failed"},`
	}
	return `{"reference_name":"` + ref + `","state":"` + state + `","series":[` + strings.Join(series, ",") + `],` +
		statusText + `"is_partial":` + partialText + `,"invalid_points":0}`
}

// namedAnswer is a named_outputs/v1 answer whose top level, as the provider
// writes it, gathers every output's state: an optional output that failed
// makes it partial.
func namedAnswer(outputs ...string) string {
	return `{"contract_version":"named_outputs/v1","outputs":[` + strings.Join(outputs, ",") + `],` +
		`"status":{"code":"ERROR","message":"output failed"},"is_partial":true,"result_table_id":["system.cpu"]}`
}

var (
	hostOne = []string{"bk_target_ip"}
	ipOne   = []string{"192.0.2.1"}
	ipTwo   = []string{"192.0.2.2"}
)

func decodeNamed(t *testing.T, attempt execution.QueryAttempt, body string) (execution.ProviderCompletion, *collectingSink) {
	t.Helper()
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(context.Background(), strings.NewReader(body), attempt, sink)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return completion, sink
}

// valuesAt is the aligned outputs of the record the sink got for the host at
// the source time, by ref.
func valuesAt(t *testing.T, completion execution.ProviderCompletion, sink *collectingSink, ip string, at int64) map[string]execution.NamedOutputValue {
	t.Helper()
	if completion.NamedOutputs == nil {
		t.Fatal("no named outputs on the completion")
	}
	for _, batch := range sink.batches {
		for index := 0; index < batch.Dataset.Len(); index++ {
			record, _ := batch.Dataset.Record(index)
			host, _ := record.Dimensions()["bk_target_ip"]
			if string(host) != `"`+ip+`"` || record.SourceTime() != at {
				continue
			}
			values, found := completion.NamedOutputs.Records[record.RecordID()]
			if !found || len(values) != len(completion.NamedOutputs.Refs) {
				t.Fatalf("record %s at %d has outputs %+v", ip, at, values)
			}
			byRef := map[string]execution.NamedOutputValue{}
			for position, ref := range completion.NamedOutputs.Refs {
				byRef[ref] = values[position]
			}
			return byRef
		}
	}
	t.Fatalf("no record for %s at %d", ip, at)
	return nil
}

func wantOutput(t *testing.T, values map[string]execution.NamedOutputValue, ref, value string, state execution.NamedOutputState) {
	t.Helper()
	if got := values[ref]; string(got.Value) != value || got.State != state {
		t.Errorf("%s = %s/%s, want %s/%s", ref, got.Value, got.State, value, state)
	}
}

// Every output is aligned to each record the legacy output delivered by its
// time and labels, __name__ aside: found, its value in its own state; not
// found, MISSING, or its own state when it failed. The query completes as
// its legacy output did - the answer's top level, partial for the output
// that failed, does not decide it - and the records are the legacy
// output's alone, __name__ kept on them.
func TestNamedOutputsAreAlignedToTheLegacyRecords(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB), namedAnswer(
		namedOutput("A", "SUCCESS", false, "",
			namedSeries([]string{"__name__", "bk_target_ip"}, []string{"usage", "192.0.2.1"}, `[[1700123060000,1],[1700123120000,2]]`),
			namedSeries([]string{"__name__", "bk_target_ip"}, []string{"usage", "192.0.2.2"}, `[[1700123060000,5]]`)),
		namedOutput("B", "ERROR", true, "ERROR"),
		namedOutput("C", "SUCCESS", false, "",
			namedSeries([]string{"bk_target_ip", "__name__"}, []string{"192.0.2.1", "sum"}, `[[1700123060000,11],[1700123120000,12]]`),
			namedSeries(hostOne, ipTwo, `[[1700123060000,15],[1700123120000,16]]`))))
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData ||
		completion.Stats.Series != 2 || completion.Stats.Records != 4 || len(sink.batches) != 2 {
		t.Fatalf("completion %+v, %d batches", completion, len(sink.batches))
	}
	if refs := completion.NamedOutputs.Refs; strings.Join(refs, ",") != "A,B,C" || completion.NamedOutputs.LegacyRef != "C" ||
		len(completion.NamedOutputs.Records) != 4 {
		t.Fatalf("named outputs %+v", completion.NamedOutputs)
	}
	first := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, first, "A", "1", execution.NamedOutputSuccess)
	wantOutput(t, first, "B", "", execution.NamedOutputError)
	wantOutput(t, first, "C", "11", execution.NamedOutputSuccess)
	later := valuesAt(t, completion, sink, "192.0.2.2", 1_700_123_120)
	wantOutput(t, later, "A", "", execution.NamedOutputMissing)
	wantOutput(t, later, "C", "16", execution.NamedOutputSuccess)
	record, _ := sink.batches[0].Dataset.Record(0)
	if name, kept := record.Dimensions()["__name__"]; !kept || string(name) != `"sum"` || len(record.Values()) != 1 {
		t.Fatalf("legacy record %+v, values %+v", record.Dimensions(), record.Values())
	}
}

// A legacy output that answered in part is a partial query, whatever the
// others say, and its own entry says so.
func TestAPartialLegacyOutputIsAPartialQuery(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB), namedAnswer(
		namedOutput("A", "SUCCESS", false, ""), namedOutput("B", "SUCCESS_EMPTY", false, ""),
		namedOutput("C", "PARTIAL", true, "QUERY_TS_PARTIAL", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))))
	if completion.Completeness != execution.CompletenessPartial {
		t.Fatalf("completion %+v", completion)
	}
	values := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, values, "C", "11", execution.NamedOutputPartial)
	wantOutput(t, values, "A", "", execution.NamedOutputMissing)
	wantOutput(t, values, "B", "", execution.NamedOutputMissing)
}

// A legacy output the provider failed to compute fails the query, as a
// status code does: an output that timed out is not one that found nothing,
// and a query with nothing would read as no data. With or without the
// status the provider gives it.
func TestAFailedLegacyOutputFailsTheQuery(t *testing.T) {
	for _, status := range []string{"ERROR", ""} {
		completion, _ := decodeNamed(t, namedAttempt(t, namedOutputsAB), namedAnswer(
			namedOutput("A", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1]]`)),
			namedOutput("B", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,2]]`)),
			namedOutput("C", "ERROR", true, status)))
		attempt := completion.RouteFacts.Attempts[0]
		// Unavailable, never complete and empty: every binding of it is UNKNOWN.
		if completion.Completeness != execution.CompletenessUnavailable ||
			attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) || attempt.Detail != "response=status_error" ||
			completion.NamedOutputs != nil {
			t.Fatalf("status %q: completion %+v", status, completion)
		}
	}
}

// Two points of one output at one time under the same labels, __name__
// aside, cannot be aligned: the query is not used, under its own reason, as
// the platform refuses it - whether the output is an optional one or the
// legacy one.
func TestADuplicatePointFailsTheQueryByName(t *testing.T) {
	for name, answer := range map[string]string{
		"optional": namedAnswer(
			namedOutput("A", "SUCCESS", false, "",
				namedSeries([]string{"__name__", "bk_target_ip"}, []string{"usage", "192.0.2.1"}, `[[1700123060000,1]]`),
				namedSeries([]string{"__name__", "bk_target_ip"}, []string{"idle", "192.0.2.1"}, `[[1700123060000,2]]`)),
			namedOutput("B", "SUCCESS", false, ""),
			namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))),
		"legacy": namedAnswer(
			namedOutput("A", "SUCCESS", false, ""), namedOutput("B", "SUCCESS", false, ""),
			namedOutput("C", "SUCCESS", false, "",
				namedSeries([]string{"__name__", "bk_target_ip"}, []string{"usage", "192.0.2.1"}, `[[1700123060000,11]]`),
				namedSeries([]string{"__name__", "bk_target_ip"}, []string{"idle", "192.0.2.1"}, `[[1700123060000,12]]`))),
	} {
		completion, _ := decodeNamed(t, namedAttempt(t, namedOutputsAB), answer)
		attempt := completion.RouteFacts.Attempts[0]
		if completion.Completeness != execution.CompletenessUnavailable || completion.NamedOutputs != nil ||
			attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryNamedOutputDuplicateKey) ||
			attempt.Detail != "response=named_output_duplicate_key" {
			t.Errorf("%s: completion %+v", name, completion)
		}
	}
}

// A provider older than the contract answers the legacy output alone, as
// if nothing was asked: the query reads it as it always did, and every
// other output is UNSUPPORTED.
func TestAnAnswerUnderNoContractLeavesTheOtherOutputsUnsupported(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB),
		`{"series":[`+namedSeries(hostOne, ipOne, `[[1700123060000,11]]`)+`],"is_partial":false}`)
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData {
		t.Fatalf("completion %+v", completion)
	}
	values := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, values, "A", "", execution.NamedOutputUnsupported)
	wantOutput(t, values, "B", "", execution.NamedOutputUnsupported)
	wantOutput(t, values, "C", "11", execution.NamedOutputSuccess)
}

// An answer under a contract other than the one asked for, or one without
// the legacy output, is not read.
func TestAnAnswerBreakingTheContractIsNotUsed(t *testing.T) {
	for name, answer := range map[string]string{
		"contract": strings.Replace(namedAnswer(namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))),
			"named_outputs/v1", "named_outputs/v2", 1),
		"no legacy": namedAnswer(namedOutput("A", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1]]`))),
		"twice": namedAnswer(namedOutput("A", "SUCCESS", false, ""), namedOutput("A", "SUCCESS", false, ""),
			namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))),
	} {
		completion, _ := decodeNamed(t, namedAttempt(t, namedOutputsAB), answer)
		if completion.Completeness != execution.CompletenessUnavailable || completion.NamedOutputs != nil ||
			completion.RouteFacts.Attempts[0].Detail != "response=named_output_contract" {
			t.Errorf("%s: completion %+v", name, completion)
		}
	}
}

// A query that asked for no named outputs reads an answer as it always
// did: named fields it did not ask for are ignored, and its completion
// carries nothing beside its records.
func TestAQueryWithoutNamedOutputsCarriesNothingBesideItsRecords(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, ""),
		`{"contract_version":"named_outputs/v1","outputs":[`+namedOutput("C", "SUCCESS", false, "")+`],`+
			`"series":[`+namedSeries(hostOne, ipOne, `[[1700123060000,11]]`)+`],"is_partial":false}`)
	if completion.Completeness != execution.CompletenessFull || completion.NamedOutputs != nil || len(sink.batches) != 1 {
		t.Fatalf("completion %+v", completion)
	}
	encoded, err := json.Marshal(completion)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "NamedOutputs") {
		t.Fatalf("completion encodes the named outputs: %s", encoded)
	}
}

// The alignment keys labels as the platform does
// (extract_unify_query_series_dimensions, _index_named_output_records): a
// group key's _tableN suffix is stripped, so an output that names a label
// with it meets the record that names it without; and __name__ is left out,
// so a bare selector meets the expression over it.
func TestTheAlignmentKeysLabelsAsThePlatformDoes(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB), namedAnswer(
		namedOutput("A", "SUCCESS", false, "",
			namedSeries([]string{"bk_target_ip_table0"}, ipOne, `[[1700123060000,1]]`)),
		namedOutput("B", "SUCCESS", false, "",
			namedSeries([]string{"__name__", "bk_target_ip"}, []string{"idle", "192.0.2.1"}, `[[1700123060000,2]]`)),
		namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))))
	values := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, values, "A", "1", execution.NamedOutputSuccess)
	wantOutput(t, values, "B", "2", execution.NamedOutputSuccess)
	if labels := alignLabels(responseSeries{GroupKeys: []string{"x_table1", "__name__", "a"},
		GroupValues: []json.RawMessage{json.RawMessage(`"1"`), json.RawMessage(`"m"`), json.RawMessage(`"2"`)}}); labels != "a\x00\"2\"\x01x\x00\"1\"" {
		t.Fatalf("labels %q", labels)
	}
}

// A state this build does not know is read as ERROR, found or not: a value
// in it is not shown as a success.
func TestAnOutputInAStateThisBuildDoesNotKnowReadsAsError(t *testing.T) {
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB), namedAnswer(
		namedOutput("A", "STALE", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1]]`)),
		namedOutput("B", "STALE", false, ""),
		namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11]]`))))
	values := valuesAt(t, completion, sink, "192.0.2.1", 1_700_123_060)
	wantOutput(t, values, "A", "1", execution.NamedOutputError)
	wantOutput(t, values, "B", "", execution.NamedOutputError)
}

// testReserver is a Slot's room for an alignment: it grants reservations
// up to limit and keeps the peak of what it granted.
type testReserver struct {
	limit, held, peak uint64
	refused           int
}

func (reserver *testReserver) ReserveRetainedBytes(_ context.Context, bytes uint64) error {
	if reserver.held+bytes > reserver.limit {
		reserver.refused++
		return errors.New("refused")
	}
	reserver.held += bytes
	reserver.peak = max(reserver.peak, reserver.held)
	return nil
}

func (reserver *testReserver) ReleaseRetainedBytes(bytes uint64) {
	reserver.held -= min(bytes, reserver.held)
}

// reservingSink is a sink a query's series go to that reserves through
// reserver, as the access source's does through its Slot.
type reservingSink struct {
	*collectingSink
	*testReserver
}

func decodeReserving(t *testing.T, body string, reserver *testReserver) execution.ProviderCompletion {
	t.Helper()
	client := &Client{limits: DefaultLimits(), now: time.Now}
	completion, err := client.decode(context.Background(), strings.NewReader(body), namedAttempt(t, namedOutputsAB),
		reservingSink{collectingSink: &collectingSink{}, testReserver: reserver})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return completion
}

func reservingAnswer() string {
	return namedAnswer(
		namedOutput("A", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1],[1700123120000,2]]`)),
		namedOutput("B", "SUCCESS", false, ""),
		namedOutput("C", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,11],[1700123120000,12]]`)))
}

// The alignment reserves what it holds through its Slot as it holds it:
// at its peak the index, the kept legacy points and the table being built
// side by side; once the answer is read, the table alone, which the
// completion carries and the Slot then holds.
func TestANamedOutputsAlignmentReservesItsPeakAndKeepsItsTable(t *testing.T) {
	reserver := &testReserver{limit: 1 << 20}
	completion := decodeReserving(t, reservingAnswer(), reserver)
	table := completion.NamedOutputs.RetainedBytes()
	if completion.NamedOutputs == nil || reserver.held != table {
		t.Fatalf("reserved %d after the answer, want the table's %d", reserver.held, table)
	}
	// The least room the answer is aligned in is its peak: two indexed
	// points, two kept points and the two records of the table being built,
	// side by side. Reservations run a chunk ahead and fall back to what is
	// held, so the least room that succeeds is what was held at the peak.
	least := sort.Search(1<<16, func(limit int) bool {
		return decodeReserving(t, reservingAnswer(), &testReserver{limit: uint64(limit)}).NamedOutputs != nil
	})
	if floor := table + 2*namedIndexPointBytes + 2*namedLegacyPointBytes; uint64(least) < floor {
		t.Fatalf("aligned in %d bytes, want at least the index, the points and the table, %d", least, floor)
	}
}

// An alignment its Slot refuses to reserve for is refused at once, as an
// answer too large to hold, and gives back everything it had reserved; so
// does an answer that fails for any other reason.
func TestARefusedOrFailedAlignmentLeavesNothingReserved(t *testing.T) {
	refusing := &testReserver{limit: 300}
	completion := decodeReserving(t, reservingAnswer(), refusing)
	if completion.Completeness != execution.CompletenessUnavailable || completion.NamedOutputs != nil ||
		completion.RouteFacts.Attempts[0].Detail != "response=limit_response_bytes" || refusing.refused == 0 || refusing.held != 0 {
		t.Fatalf("held %d refused %d completion %+v", refusing.held, refusing.refused, completion)
	}
	failing := &testReserver{limit: 1 << 20}
	completion = decodeReserving(t, namedAnswer(
		namedOutput("A", "SUCCESS", false, "", namedSeries(hostOne, ipOne, `[[1700123060000,1]]`)),
		namedOutput("C", "ERROR", true, "ERROR")), failing)
	if completion.Completeness != execution.CompletenessUnavailable || failing.peak == 0 || failing.held != 0 {
		t.Fatalf("a failed answer holds %d (peak %d): %+v", failing.held, failing.peak, completion)
	}
}

// heapMeasureEnv set runs TestTheNamedOutputHoldingsAreWhatTheyCost's
// measuring itself; unset, the test runs it in a process of its own.
const heapMeasureEnv = "ALARMD_TEST_HEAP_MEASURE"

// What the alignment is charged while an answer is read is at least what it
// measures on the heap, and not much more: an indexed point of an output,
// and a delivered point of the legacy output beside its RecordID.
//
// The heap it reads is the whole process's: in the package's own process, a
// goroutine an earlier test left behind freeing memory during a build read
// as the build costing less (79 bytes a point against 87-88 alone, past the
// quarter). So the measuring runs in a process of its own.
func TestTheNamedOutputHoldingsAreWhatTheyCost(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("the race detector changes what an allocation costs")
	}
	if os.Getenv(heapMeasureEnv) == "" {
		measure := exec.Command(os.Args[0], "-test.run=^TestTheNamedOutputHoldingsAreWhatTheyCost$", "-test.count=1", "-test.v")
		measure.Env = append(os.Environ(), heapMeasureEnv+"=1")
		out, err := measure.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "indexed point: measured") {
			t.Fatalf("measured in a process of its own: %v\n%s", err, out)
		}
		t.Logf("measured in a process of its own:\n%s", out)
		return
	}
	const points = 50000
	labels := alignLabels(responseSeries{GroupKeys: hostOne, GroupValues: []json.RawMessage{json.RawMessage(`"192.0.2.1"`)}})
	var charged uint64
	var index map[alignKey]json.RawMessage
	measured := heapGrowth(func() any {
		index = map[alignKey]json.RawMessage{}
		for point := 0; point < points; point++ {
			value := json.RawMessage(strconv.Itoa(point))
			charged += namedIndexPointBytes + uint64(len(value))
			index[alignKey{time: int64(point), labels: labels}] = value
		}
		return index
	})
	wantCharged(t, "indexed point", charged, measured, points)
	charged = 0
	var legacy []legacyPoint
	measured = heapGrowth(func() any {
		for point := 0; point < points; point++ {
			recordID, value := fmt.Sprintf("%064x", point), json.RawMessage(strconv.Itoa(point))
			charged += namedLegacyPointBytes + uint64(len(recordID)+len(value))
			legacy = append(legacy, legacyPoint{recordID: recordID, key: alignKey{time: int64(point), labels: labels}, value: value})
		}
		return legacy
	})
	wantCharged(t, "legacy point", charged, measured, points)
}

func heapGrowth(build func() any) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	kept := build()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(kept)
	if after.HeapAlloc <= before.HeapAlloc {
		// The collector freed more than the build kept: no measure.
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

func wantCharged(t *testing.T, what string, charged, measured, count uint64) {
	t.Helper()
	if measured == 0 {
		t.Fatalf("%s: the heap did not grow across the build, nothing was measured", what)
	}
	t.Logf("%s: measured %d bytes, charged %d", what, measured/count, charged/count)
	if charged < measured || charged > measured+measured/4 {
		t.Errorf("%s: charged %d, measured %d: want at least the measure and within a quarter of it", what, charged, measured)
	}
}

// A query without named outputs reserves nothing, through a sink that
// would reserve.
func TestAQueryWithoutNamedOutputsReservesNothing(t *testing.T) {
	reserver := &testReserver{limit: 1 << 20}
	client := &Client{limits: DefaultLimits(), now: time.Now}
	if _, err := client.decode(context.Background(), strings.NewReader(
		`{"series":[`+namedSeries(hostOne, ipOne, `[[1700123060000,11]]`)+`],"is_partial":false}`),
		namedAttempt(t, ""), reservingSink{collectingSink: &collectingSink{}, testReserver: reserver}); err != nil {
		t.Fatal(err)
	}
	if reserver.peak != 0 {
		t.Fatalf("a query without named outputs reserved %d", reserver.peak)
	}
}

// A Slot with less room than a reservation chunk left still aligns an
// answer that fits: the chunk ahead is refused and the alignment reserves
// exactly what it holds instead, so a Slot near its share or the pool is not
// refused every alignment however small.
func TestAnAlignmentFitsInLessRoomThanAChunk(t *testing.T) {
	reserver := &testReserver{limit: namedReserveChunk / 2}
	completion := decodeReserving(t, reservingAnswer(), reserver)
	if completion.NamedOutputs == nil || reserver.refused == 0 {
		t.Fatalf("aligned %v with %d refusals in %d bytes of room", completion.NamedOutputs != nil, reserver.refused, reserver.limit)
	}
	if reserver.held != completion.NamedOutputs.RetainedBytes() || reserver.peak >= namedReserveChunk/2 {
		t.Fatalf("held %d (peak %d), want the table's %d and never a chunk", reserver.held, reserver.peak, completion.NamedOutputs.RetainedBytes())
	}
}
