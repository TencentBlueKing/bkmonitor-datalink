package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// procPortRow is one provider row of a process: the proc_exists value and the
// five dynamic port dimensions, encoded as the UQ client delivers them.
type procPortRow struct {
	value                                                  string
	nonListen, notAccurateListen, listen, protocol, bindIP string
}

func (row procPortRow) record(base contract.CanonicalRecordV2) contract.CanonicalRecordV2 {
	record := base
	record.Values = map[string]json.RawMessage{"value": json.RawMessage(row.value)}
	record.Dimensions = map[string]json.RawMessage{
		"bk_target_cloud_id": json.RawMessage(`"0"`), "bk_target_ip": json.RawMessage(`"10.0.0.9"`), "display_name": json.RawMessage(`"nginx"`),
		"nonlisten": json.RawMessage(row.nonListen), "not_accurate_listen": json.RawMessage(row.notAccurateListen),
		"listen": json.RawMessage(row.listen), "protocol": json.RawMessage(row.protocol), "bind_ip": json.RawMessage(row.bindIP),
	}
	return record
}

// procPortRowBatches turns the fixture's single PRIMARY batch into one batch
// per provider row of the same process and source time, exactly what the UQ
// client streams when the query grouping is finer than the series identity.
func procPortRowBatches(t *testing.T, base execution.SeriesExecutionBatch, rows []procPortRow) ([]execution.SeriesExecutionBatch, execution.SeriesDelivery) {
	t.Helper()
	template := base.Dataset.Records()[0]
	batches := make([]execution.SeriesExecutionBatch, 0, len(rows))
	var delivery execution.SeriesDelivery
	for index, row := range rows {
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{row.record(template)})
		view, err := execution.NewDatasetView(dataset, []uint32{0})
		if err != nil {
			t.Fatal(err)
		}
		inputs := make([]execution.NamedInputBinding, len(base.Inputs))
		for position, binding := range base.Inputs {
			binding.Dataset, binding.View = dataset, view
			inputs[position] = binding
		}
		batch := base
		batch.Dataset, batch.Inputs = dataset, inputs
		batch.Delivery.Digest = strings.Repeat(string(rune('a'+index)), 64)
		batches = append(batches, batch)
		delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery)
		if err != nil {
			t.Fatal(err)
		}
	}
	return batches, delivery
}

func primaryEvaluationBinding(t *testing.T, evaluator *recordingEvaluator) execution.NamedInputBinding {
	t.Helper()
	if len(evaluator.requests) != 1 || len(evaluator.requests[0].Inputs) != 1 {
		t.Fatalf("evaluation requests=%d, want exactly one series evaluation", len(evaluator.requests))
	}
	for _, binding := range evaluator.requests[0].Inputs[0].Inputs {
		if binding.Role == execution.InputRolePrimary {
			return binding
		}
	}
	t.Fatal("evaluation lacks a PRIMARY binding")
	return execution.NamedInputBinding{}
}

// The ProcPort production failure: the query groups by the dynamic port
// dimensions the identity excludes, so one process with two port rows reaches
// the worker as two batches for one (consumer, series, requirement) key. The
// worker folds them under the policy the compiled ProcPort algorithm declares.
// Python evaluates every row and any anomalous row raises the anomaly, so the
// folded record must be ABNORMAL whenever one row is, whichever row it is,
// carry the offending ports, and stay NORMAL when every row is healthy.
func TestSlotExecutionCoordinatorFoldsProcPortRowsPreservingEveryAnomalousRow(t *testing.T) {
	healthyTCP := procPortRow{value: `1`, nonListen: `"[]"`, notAccurateListen: `"[]"`, listen: `"[80]"`, protocol: `"tcp"`, bindIP: `"10.0.0.1"`}
	healthyUDP := procPortRow{value: `1`, nonListen: `"[]"`, notAccurateListen: `"null"`, listen: `"[53]"`, protocol: `"udp"`, bindIP: `"10.0.0.1"`}
	portsMissing := procPortRow{value: `1`, nonListen: `"[8080, 8081]"`, notAccurateListen: `"[]"`, listen: `"[]"`, protocol: `"tcp"`, bindIP: `"10.0.0.2"`}
	bindMismatch := procPortRow{value: `1`, nonListen: `"[]"`, notAccurateListen: `"[127.0.0.1:10011]"`, listen: `"[10011]"`, protocol: `"tcp"`, bindIP: `"10.0.0.3"`}
	processGone := procPortRow{value: `0`, nonListen: `"[]"`, notAccurateListen: `"[]"`, listen: `"[]"`, protocol: `"tcp"`, bindIP: `"10.0.0.1"`}
	tests := []struct {
		name          string
		rows          []procPortRow
		wantOutcome   execution.LevelOutcomeKind
		wantValue     string
		wantNonListen string
		wantMismatch  string
		wantListen    string
		wantProtocol  string
		wantBindIP    string
	}{
		{
			name: "first row anomalous", rows: []procPortRow{portsMissing, healthyTCP}, wantOutcome: execution.LevelOutcomeAbnormal,
			wantValue: `1`, wantNonListen: `"[8080, 8081]"`, wantMismatch: `"[]"`, wantListen: `"[80]"`, wantProtocol: `"tcp"`, wantBindIP: `["10.0.0.2","10.0.0.1"]`,
		},
		{
			name: "second row anomalous", rows: []procPortRow{healthyTCP, portsMissing}, wantOutcome: execution.LevelOutcomeAbnormal,
			wantValue: `1`, wantNonListen: `"[8080, 8081]"`, wantMismatch: `"[]"`, wantListen: `"[80]"`, wantProtocol: `"tcp"`, wantBindIP: `["10.0.0.1","10.0.0.2"]`,
		},
		{
			name: "two healthy rows", rows: []procPortRow{healthyTCP, healthyUDP}, wantOutcome: execution.LevelOutcomeNormal,
			wantValue: `1`, wantNonListen: `"[]"`, wantMismatch: `"[]"`, wantListen: `"[80,53]"`, wantProtocol: `["tcp","udp"]`, wantBindIP: `"10.0.0.1"`,
		},
		{
			name: "listen address mismatch in the second row", rows: []procPortRow{healthyTCP, bindMismatch}, wantOutcome: execution.LevelOutcomeAbnormal,
			wantValue: `1`, wantNonListen: `"[]"`, wantMismatch: `"[127.0.0.1:10011]"`, wantListen: `"[80,10011]"`, wantProtocol: `"tcp"`, wantBindIP: `["10.0.0.1","10.0.0.3"]`,
		},
		{
			name: "process absent in one row", rows: []procPortRow{healthyTCP, processGone}, wantOutcome: execution.LevelOutcomeAbnormal,
			wantValue: `0`, wantNonListen: `"[]"`, wantMismatch: `"[]"`, wantListen: `"[80]"`, wantProtocol: `"tcp"`, wantBindIP: `"10.0.0.1"`,
		},
		{
			name: "three rows union their ports", rows: []procPortRow{portsMissing, healthyTCP, {value: `1`, nonListen: `"[8081, 9000]"`, notAccurateListen: `"[]"`, listen: `"[]"`, protocol: `"udp"`, bindIP: `"10.0.0.2"`}},
			wantOutcome: execution.LevelOutcomeAbnormal,
			wantValue:   `1`, wantNonListen: `"[8080,8081,9000]"`, wantMismatch: `"[]"`, wantListen: `"[80]"`, wantProtocol: `["tcp","udp"]`, wantBindIP: `["10.0.0.2","10.0.0.1"]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, batches := workerG4StreamFixture(t, strategy.DetectorKindProcPort)
			rowBatches, delivery := procPortRowBatches(t, batches[0], test.rows)
			completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{{
				Ref: batches[0].CompletionRef, PhysicalQuery: batches[0].PhysicalQuery, QueryRevision: batches[0].QueryRevision,
				Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery,
			}}}
			completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)
			recorder := &queryCompletedRecorder{}
			ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, recorder)
			ports.executeOverride = streamExecution(header, rowBatches, completion)

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v, want the folded process evaluated", result, err)
			}
			if len(evaluator.results) != 1 || len(evaluator.results[0].Plans) != 1 || len(evaluator.results[0].Plans[0].LevelOutcomes) != 1 {
				t.Fatalf("evaluation results=%+v, want one Level outcome", evaluator.results)
			}
			plan := evaluator.results[0].Plans[0]
			// The fixture's series state is cold, so a healthy first Slot is a
			// WARMING UNKNOWN outcome; the detect fact written to State shows the
			// folded record's verdict for every case, the outcome for ABNORMAL.
			wantFact, wantEvents := execution.LevelFactNormal, 0
			if test.wantOutcome == execution.LevelOutcomeAbnormal {
				wantFact, wantEvents = execution.LevelFactAnomalous, 1
				if plan.LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal {
					t.Fatalf("Level outcome=%+v, want ABNORMAL", plan.LevelOutcomes[0])
				}
			} else if plan.LevelOutcomes[0].Outcome == execution.LevelOutcomeAbnormal {
				t.Fatalf("Level outcome=%+v, want no anomaly", plan.LevelOutcomes[0])
			}
			if len(plan.StateResults) != 1 || len(plan.StateResults[0].Mutation.Points) != 1 || len(plan.StateResults[0].Mutation.Points[0].Levels) != 1 ||
				plan.StateResults[0].Mutation.Points[0].Levels[0].Result != wantFact {
				t.Fatalf("state results=%+v, want one detect fact %s", plan.StateResults, wantFact)
			}
			if ports.eventCount != wantEvents {
				t.Fatalf("events=%d, want %d", ports.eventCount, wantEvents)
			}
			primary := primaryEvaluationBinding(t, evaluator)
			if primary.View.Len() != 1 {
				t.Fatalf("folded PRIMARY view has %d records, want one per source time", primary.View.Len())
			}
			record, _ := primary.View.Record(0)
			got := map[string]string{
				"value": string(record.Values()["value"]), "nonlisten": string(record.Dimensions()["nonlisten"]),
				"not_accurate_listen": string(record.Dimensions()["not_accurate_listen"]), "listen": string(record.Dimensions()["listen"]),
				"protocol": string(record.Dimensions()["protocol"]), "bind_ip": string(record.Dimensions()["bind_ip"]),
				"display_name": string(record.Dimensions()["display_name"]),
			}
			want := map[string]string{
				"value": test.wantValue, "nonlisten": test.wantNonListen, "not_accurate_listen": test.wantMismatch,
				"listen": test.wantListen, "protocol": test.wantProtocol, "bind_ip": test.wantBindIP, "display_name": `"nginx"`,
			}
			for field, wantValue := range want {
				if got[field] != wantValue {
					t.Fatalf("folded %s = %s, want %s (all fields %v)", field, got[field], wantValue, got)
				}
			}
			for _, observation := range recorder.observations {
				if observation.Result == observability.ResultFailed {
					t.Fatalf("query_completed reported a failure: %+v", observation)
				}
			}
		})
	}
}

// A plan whose provider rows are finer than its identity but whose algorithm
// declares no fold policy must keep failing deterministically: nothing may be
// folded silently under an arbitrary tie-break.
func TestSlotExecutionCoordinatorStillRejectsDuplicateSeriesWithoutFoldPolicy(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	primary, previous := shareFixtureQueries(t, header)
	duplicated := batches[primary.index]
	duplicated.Delivery.Digest = strings.Repeat("9", 64)
	delivery, err := execution.AccumulateSeriesDelivery(batches[primary.index].Delivery, duplicated.Delivery)
	if err != nil {
		t.Fatal(err)
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
		{Ref: batches[primary.index].CompletionRef, PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery},
		{Ref: batches[previous.index].CompletionRef, PhysicalQuery: previous.query.Digest, QueryRevision: previous.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: batches[previous.index].Delivery},
	}}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)
	recorder := &queryCompletedRecorder{}
	ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, recorder)
	ports.executeOverride = streamExecution(header, []execution.SeriesExecutionBatch{batches[primary.index], duplicated, batches[previous.index]}, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err == nil || result.Completed || !strings.Contains(err.Error(), "alarmd worker: duplicate streamed named input") {
		t.Fatalf("Execute() result=%+v error=%v, want the duplicate rejected", result, err)
	}
	if len(evaluator.requests) != 0 || ports.stateApplyCalls != 0 || ports.eventCount != 0 {
		t.Fatalf("rejected duplicate reached evaluation or side effects: evaluations=%d state=%d events=%d",
			len(evaluator.requests), ports.stateApplyCalls, ports.eventCount)
	}
	observation := recorder.last(t)
	if observation.QueryFailure == nil || observation.QueryFailure.Stage != "execute" || observation.QueryFailure.Category != "named_input" ||
		observation.QueryFailure.Code != "STREAMED_NAMED_INPUT_DUPLICATE" {
		t.Fatalf("failure facts=%+v, want execute named_input STREAMED_NAMED_INPUT_DUPLICATE", observation.QueryFailure)
	}
}
