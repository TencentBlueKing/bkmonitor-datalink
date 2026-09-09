package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const flowQG = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestTargetFlowQueueNoisePreservesExecutionBudget(t *testing.T) {
	f, b := newTestFlow(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
			}
		}()
	}
	wg.Wait()
	for _, stage := range []string{"runner_dispatch", "slot_selection", "query_completed", "progress_committed", "execution_outcome"} {
		f.Record(stage, flowQG, TargetFlowFacts{})
	}
	if f.records != 6 || f.dropped != 0 {
		t.Fatalf("queue noise consumed execution quota: records=%d dropped=%d", f.records, f.dropped)
	}
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	var last map[string]interface{}
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	if last["queue_suppressed_total"] != float64(15999) {
		t.Fatalf("suppression facts missing: %+v", last)
	}
}

func TestTargetFlowQueueThrottleScopesAndWindow(t *testing.T) {
	f, _ := newTestFlow(t)
	brother := strings.Repeat("b", 64)
	if err := f.Select([]string{flowQG, brother}); err != nil {
		t.Fatal(err)
	}
	for _, qg := range []string{flowQG, brother} {
		for _, reason := range []string{"normal_queue_full", "delayed_queue_full", "delayed_queue_evicted"} {
			for i := 0; i < 2; i++ {
				f.Record("queue_skipped", qg, TargetFlowFacts{Decision: reason})
			}
		}
	}
	if f.records != 6 {
		t.Fatalf("QG/reason first occurrence lost: %d", f.records)
	}
	// Unrecognized reasons and other stages are not silently suppressed.
	for i := 0; i < 2; i++ {
		f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "unknown"})
		f.Record("runner_queued", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
	}
	if f.records != 10 {
		t.Fatal("throttle escaped its finite queue-rejection scope")
	}
	f.now = func() time.Time { return time.Unix(161, 0) }
	f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
	if f.records != 1 {
		t.Fatal("new window first occurrence suppressed")
	}
	f.now = func() time.Time { return time.Unix(90, 0) }
	f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
	if f.records != 1 {
		t.Fatal("clock rollback retained stale suppression")
	}
}

func BenchmarkTargetFlowRepeatedQueueRejection(b *testing.B) {
	f, _ := NewTargetFlow(New("runtime", io.Discard), TargetFlowConfig{QueryGroups: []string{flowQG}})
	f.now = func() time.Time { return time.Unix(100, 0) }
	f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Record("queue_skipped", flowQG, TargetFlowFacts{Decision: "normal_queue_full"})
	}
}

func newTestFlow(t *testing.T) (*TargetFlow, *bytes.Buffer) {
	t.Helper()
	b := new(bytes.Buffer)
	f, e := NewTargetFlow(New("runtime", b), TargetFlowConfig{QueryGroups: []string{flowQG}})
	if e != nil {
		t.Fatal(e)
	}
	f.now = func() time.Time { return time.Unix(100, 0) }
	return f, b
}
func TestTargetFlowSelectedSuccessAndBrotherIsolation(t *testing.T) {
	f, b := newTestFlow(t)
	ctx := f.Context(context.Background(), flowQG)
	for i := 0; i < 10000; i++ {
		f.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess, Trace: TraceFields{QueryGroupKey: "brother"}})
	}
	if b.Len() != 0 || f.records != 0 {
		t.Fatal("brother consumed diagnostic quota")
	}
	for _, stage := range []Stage{StageSlotStarted, StageQueryCompleted, StageProgressCommitted, StageSlotCompleted} {
		f.Observe(ctx, Observation{Component: ComponentScheduler, Stage: stage, Result: ResultSuccess, Trace: TraceFields{QueryGroupKey: flowQG}})
	}
	if f.records != 4 {
		t.Fatalf("success sequence lost: %d", f.records)
	}
	for i := 0; i < 500; i++ {
		f.Observe(ctx, Observation{Stage: StageEvaluationCompleted})
	}
	if f.records != 4 {
		t.Fatal("per-point logging")
	}
	EmitTargetFlow(ctx, "runner_decision", TraceFields{}, TargetFlowFacts{Completed: true})
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	var last targetFlowRecord
	if e := json.Unmarshal(lines[len(lines)-1], &last); e != nil {
		t.Fatal(e)
	}
	if last.Facts.Evaluations != 500 || !last.Diagnostic {
		t.Fatalf("missing bounded aggregate: %+v", last)
	}
}
func TestTargetFlowGlobalRecordAndByteBoundsIncludingDrop(t *testing.T) {
	f, b := newTestFlow(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
			}
		}()
	}
	wg.Wait()
	if f.records > TargetFlowMaxRecords || b.Len() > TargetFlowMaxBytes || !bytes.Contains(b.Bytes(), []byte("target_flow_dropped")) {
		t.Fatalf("unbounded or invisible drop records=%d bytes=%d", f.records, b.Len())
	}
	if f.records != len(bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))) || f.bytes != b.Len() {
		t.Fatal("budget excludes emitted bytes/marker")
	}
	oldDropped := f.dropped
	f.now = func() time.Time { return time.Unix(161, 0) }
	f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	var last targetFlowRecord
	json.Unmarshal(lines[len(lines)-1], &last)
	if last.Dropped != oldDropped {
		t.Fatal("next window lost cumulative drops")
	}
	f2, b2 := newTestFlow(t)
	tr := TraceFields{QueryGroupKey: flowQG, StrategyID: strings.Repeat("x", 128), SnapshotRevision: strings.Repeat("s", 128), QueryRevision: strings.Repeat("q", 128), ScheduleRevision: strings.Repeat("r", 128), OwnerID: strings.Repeat("o", 128)}
	for i := 0; i < TargetFlowMaxRecords; i++ {
		f2.emit("slot_selection", "success", "", tr, TargetFlowFacts{BusinessID: strings.Repeat("b", 128), TenantID: strings.Repeat("t", 128)}, 0)
	}
	if f2.bytes > TargetFlowMaxBytes || f2.records >= TargetFlowMaxRecords || !bytes.Contains(b2.Bytes(), []byte("target_flow_dropped")) {
		t.Fatal("byte bound not exercised")
	}
}

func TestTargetFlowReservesBudgetForCriticalCompletionStages(t *testing.T) {
	f, b := newTestFlow(t)
	for i := 0; i < TargetFlowMaxRecords*2; i++ {
		f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
	}
	if f.windowDropped == 0 {
		t.Fatal("non-critical flow did not reach its reserve boundary")
	}
	droppedBeforeCritical := f.dropped
	for i := 0; i < 960; i++ {
		f.emit("progress_committed", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{Completed: true}, 0)
	}
	output := b.String()
	if got := strings.Count(output, `"stage":"progress_committed"`); got != 960 {
		t.Fatalf("critical completion records after non-critical saturation = %d, want 960", got)
	}
	if f.dropped != droppedBeforeCritical {
		t.Fatalf("critical completion records consumed their reserve: drops %d -> %d", droppedBeforeCritical, f.dropped)
	}
	if f.records > TargetFlowMaxRecords || f.bytes > TargetFlowMaxBytes {
		t.Fatalf("critical reserve escaped hard bounds: records=%d bytes=%d", f.records, f.bytes)
	}
}

func TestTargetFlowDropMarkerExplainsWindowAndReserve(t *testing.T) {
	f, b := newTestFlow(t)
	for i := 0; i < TargetFlowMaxRecords; i++ {
		f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
	}
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	var marker map[string]any
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["stage"] == "target_flow_dropped" {
			marker = record
		}
	}
	if marker == nil {
		t.Fatal("drop marker missing")
	}
	if marker["drop_reason"] != "critical_record_reserve" || marker["selected_query_groups"] != float64(1) {
		t.Fatalf("drop marker does not identify the reserve boundary: %+v", marker)
	}
	if marker["window_dropped"].(float64) < 1 || marker["written_records"].(float64) < 1 || marker["written_bytes"].(float64) < 1 {
		t.Fatalf("drop marker is missing window facts: %+v", marker)
	}
}

func TestTargetFlowDisabledValidationAndOversize(t *testing.T) {
	f, e := NewTargetFlow(nil, TargetFlowConfig{})
	if e != nil || f != nil {
		t.Fatal("disabled requires logger")
	}
	ctx := context.Background()
	if f.Context(ctx, flowQG) != ctx {
		t.Fatal("disabled context allocation")
	}
	for _, qs := range [][]string{{"bad"}, {flowQG, flowQG}, make([]string, 33)} {
		if (TargetFlowConfig{QueryGroups: qs}).Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
	f, b := newTestFlow(t)
	f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG, OwnerID: strings.Repeat("z", 10000)}, TargetFlowFacts{}, 0)
	if bytes.Contains(b.Bytes(), []byte(strings.Repeat("z", 100))) {
		t.Fatal("oversize identity logged")
	}
	if !bytes.Contains(b.Bytes(), []byte("target_flow_dropped")) {
		t.Fatal("oversize missing drop")
	}
}

type flowPanicWriter struct{}

func (flowPanicWriter) Write([]byte) (int, error) { panic("writer") }
func TestTargetFlowWriterPanicIsolation(t *testing.T) {
	f, e := NewTargetFlow(New("runtime", flowPanicWriter{}), TargetFlowConfig{QueryGroups: []string{flowQG}})
	if e != nil {
		t.Fatal(e)
	}
	f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
}
func BenchmarkTargetFlowBrother(b *testing.B) {
	f, _ := NewTargetFlow(New("runtime", io.Discard), TargetFlowConfig{QueryGroups: []string{flowQG}})
	o := Observation{Stage: StageQueryCompleted, Trace: TraceFields{QueryGroupKey: "brother"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Observe(context.Background(), o)
	}
}
func BenchmarkTargetFlowSelected(b *testing.B) {
	f, _ := NewTargetFlow(New("runtime", io.Discard), TargetFlowConfig{QueryGroups: []string{flowQG}})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i%1000 == 0 {
			f.mu.Lock()
			f.records = 0
			f.bytes = 0
			f.mu.Unlock()
		}
		f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
	}
}

func BenchmarkTargetFlowMixedBrother(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled_with_selected_sibling"
		}
		b.Run(name, func(b *testing.B) {
			var f *TargetFlow
			if enabled {
				f, _ = NewTargetFlow(New("runtime", io.Discard), TargetFlowConfig{QueryGroups: []string{flowQG}})
			}
			o := Observation{Stage: StageQueryCompleted, Trace: TraceFields{QueryGroupKey: "brother"}}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if enabled && i%10000 == 0 {
					f.mu.Lock()
					f.records = 0
					f.bytes = 0
					f.mu.Unlock()
				}
				if enabled && i%100 == 0 {
					f.emit("slot_selection", "success", "", TraceFields{QueryGroupKey: flowQG}, TargetFlowFacts{}, 0)
				}
				f.Observe(context.Background(), o)
			}
		})
	}
}
