package progress

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"math"
	"strings"
	"testing"
)

// A range-capable Store is required before the last Slot may write a Guard:
// the old single projection cannot retain both the head and the guarded tail.
func TestExpiredRangeHasDurableBeginBeforeTailGuard(t *testing.T) {
	store := mustStore(t, &controlFake{missing: true})
	if _, ok := any(store).(execution.ExpiredRangeStore); !ok {
		t.Fatal("Progress has no durable range Begin/Commit; tail Guard cannot recover from the head after restart")
	}
}

func rangeFixture(t *testing.T) execution.ExpiredRangeProjectionV1 {
	t.Helper()
	first, last := progressProjectionAt(60), progressProjectionAt(180)
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plans := []execution.FrozenPlanSchedule{{Identity: first.DuePlanTargets.Plans[0], ScheduleRevision: revision, Spec: spec}}
	sched, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		t.Fatal(err)
	}
	first.Contract.ScheduleRevision, last.Contract.ScheduleRevision = sched, sched
	p, err := execution.SealExpiredRange(execution.ExpiredRangeProjectionV1{
		Schedule: execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: "s", PublicationEpoch: 1},
			QueryGroup:  "q", QueryRevision: "query", ScheduleRevision: sched, Start: 60}, Plans: plans},
		First: first, Last: last, Next: 240, Count: 3, QueryReserveMillis: 59000,
		ReplayAgeMillis: 60000, JudgedAtMillis: 241000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExpiredRangeCASFailureRestartAndResponseLoss(t *testing.T) {
	ctx := context.Background()
	p := rangeFixture(t)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fake := &controlFake{value: mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 60})}
	store := mustStore(t, fake)
	request := execution.ExpiredRangeRequest{Projection: p, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}}
	if result, err := store.BeginRange(ctx, request); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("begin: %+v %v", result, err)
	}
	pending := append([]byte(nil), fake.value...)
	if !bytes.Contains(pending, []byte(schemaRangeV1)) {
		t.Fatal("pending uses silently compatible old schema")
	}
	fake.status = ownership.FencedCASConflict
	if result, err := store.CommitRange(ctx, request); err != nil || result.Status != execution.ProgressConflict {
		t.Fatalf("conflict: %+v %v", result, err)
	}
	if !bytes.Equal(pending, fake.value) {
		t.Fatal("failed CAS changed proof")
	}
	restarted := mustStore(t, fake)
	load, err := restarted.LoadProgress(ctx, identity)
	if err != nil || load.Progress.UnfinishedRange == nil || !load.Progress.UnfinishedRange.Equal(p) {
		t.Fatalf("restart: %+v %v", load, err)
	}
	fake.status = ownership.FencedCASApplied
	if result, err := restarted.CommitRange(ctx, request); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("commit: %+v %v", result, err)
	}
	committed := append([]byte(nil), fake.value...)
	result, err := restarted.CommitRange(ctx, request)
	if err != nil || !result.AlreadyCommitted || !bytes.Equal(committed, fake.value) {
		t.Fatalf("lost response replay: %+v %v", result, err)
	}
	load, err = restarted.LoadProgress(ctx, identity)
	if err != nil || load.Progress.NextSlot != 240 || load.Progress.LastFullSlot != 0 || load.Progress.CurrentOrRecentGap.Count != 3 || load.Progress.UnfinishedRange != nil {
		t.Fatalf("result: %+v %v", load, err)
	}
	if !bytes.Contains(committed, []byte(schemaV2)) || bytes.Contains(committed, []byte("UnfinishedRange")) {
		t.Fatal("completed value did not return to old v2 bytes")
	}
	var env envelope
	if err := json.Unmarshal(pending, &env); err != nil {
		t.Fatal(err)
	}
	env.Schema = schemaV2
	bad, _ := json.Marshal(env)
	if _, err := decode(bad); err == nil {
		t.Fatal("range hidden under v2 accepted")
	}
}

func TestExpiredRangeProofMutationAndLegacyBytes(t *testing.T) {
	p := rangeFixture(t)
	for name, change := range map[string]func(*execution.ExpiredRangeProjectionV1){
		"count":       func(p *execution.ExpiredRangeProjectionV1) { p.Count++ },
		"next":        func(p *execution.ExpiredRangeProjectionV1) { p.Next++ },
		"tail":        func(p *execution.ExpiredRangeProjectionV1) { p.Last.Contract.Slot.EvaluationTime++ },
		"future":      func(p *execution.ExpiredRangeProjectionV1) { p.JudgedAtMillis-- },
		"plan":        func(p *execution.ExpiredRangeProjectionV1) { p.Last.DuePlanTargets.Plans[0].StrategyID = "different" },
		"publication": func(p *execution.ExpiredRangeProjectionV1) { p.Schedule.Segment.Publication.PublicationEpoch++ },
	} {
		t.Run(name, func(t *testing.T) {
			copy := p.Clone()
			change(&copy)
			if err := copy.Validate(); err == nil {
				t.Fatal("mutated proof accepted")
			}
			if err := p.Validate(); err != nil {
				t.Fatal("caller alias changed original")
			}
		})
	}
	current := execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 60}
	got, err := encode(current)
	if err != nil {
		t.Fatal(err)
	}
	old := struct {
		Schema   string `json:"schema"`
		Progress struct {
			Identity           execution.ProgressIdentity
			NextSlot           execution.EvaluationTime
			LastFullSlot       execution.EvaluationTime
			LastCompletionKind execution.CompletionKind
			CurrentOrRecentGap *execution.ProgressGapSummary
			UnfinishedSlot     *execution.UnfinishedSlotProjection
		} `json:"progress"`
	}{Schema: schemaV2}
	old.Progress.Identity, old.Progress.NextSlot = current.Identity, current.NextSlot
	want, err := json.Marshal(old)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("old v2 bytes changed: %s / %s", got, want)
	}
}

func TestExpiredRangeOverflowAndSinglePendingRejectBeforeCAS(t *testing.T) {
	p := rangeFixture(t)
	p.First.Contract.Slot.EvaluationTime = 120
	p.First.EarliestQueryDeadlineUnixMilli = 121000
	p.First.KeepUntilUnixMilli = 721000
	p.Count = 2
	p, err := execution.SealExpiredRange(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, single := range []bool{false, true} {
		current := execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 120, LastCompletionKind: execution.CompletionSnapshotUnavailable,
			CurrentOrRecentGap: &execution.ProgressGapSummary{Kind: execution.CompletionSnapshotUnavailable, ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable), FirstSlot: 60, LastSlot: 60, Count: math.MaxUint32}}
		if single {
			projection := p.First
			current.UnfinishedSlot = &projection
		}
		raw := mustEncode(t, current)
		fake := &controlFake{value: raw}
		store := mustStore(t, fake)
		result, err := store.BeginRange(context.Background(), execution.ExpiredRangeRequest{Projection: p, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}})
		if err == nil && result.Status == execution.ProgressCommitted {
			t.Fatal("invalid new pending accepted")
		}
		if !bytes.Equal(raw, fake.value) {
			t.Fatal("rejection changed Progress")
		}
	}
}

func TestExpiredRangeWireRejectsAmbiguityBeforeResume(t *testing.T) {
	p := rangeFixture(t)
	raw := mustEncode(t, execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 60, UnfinishedRange: &p})
	for name, value := range map[string][]byte{
		"duplicate": []byte(strings.Replace(string(raw), `"Count":3`, `"Count":3,"Count":3`, 1)),
		"unknown":   []byte(strings.Replace(string(raw), `"Count":3`, `"Count":3,"future_scope":true`, 1)),
		"oversize":  append(append([]byte(nil), raw...), bytes.Repeat([]byte(" "), execution.MaxExpiredRangeProjectionBytes)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(value); err == nil {
				t.Fatal("ambiguous or oversized range accepted")
			}
		})
	}
}
