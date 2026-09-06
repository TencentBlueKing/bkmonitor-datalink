package main

import (
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

func TestPhaseTwoReceiptCountUnitsAndFailedACK(t *testing.T) {
	id := execution.PlanIdentity{TenantID: "t", BusinessID: "1", StrategyID: "2"}
	z := contract.KnownShadowCountV1(0)
	p := &phaseTwoPlanCoverage{levels: map[uint32]*contract.ShadowLevelCoverageV1{1: coverageZeroLevel(1), 9: coverageZeroLevel(9)}, outcomes: contract.ShadowRecordOutcomesV1{PrimaryAbnormal: z, PrimaryRecovery: z, NoEvent: z, Excluded: z, Unavailable: z, Terminal: z}}
	f := &phaseTwoReceiptFacts{plans: map[execution.PlanIdentity]*phaseTwoPlanCoverage{id: p}}
	for _, series := range []string{"a", "b"} {
		r := execution.PlanEvaluationResult{Plan: id}
		for _, level := range []uint32{1, 9} {
			r.LevelOutcomes = append(r.LevelOutcomes, execution.LevelOutcome{LevelID: level, SeriesIdentityDigest: execution.SeriesIdentityDigest(series), Record: execution.RecordAnchor{RecordID: "point", SourceTime: 60}, Outcome: execution.LevelOutcomeAbnormal})
		}
		event := contract.TriggerEventV1{TenantID: "t", BusinessID: "1", EventKind: contract.TriggerEventAbnormal, PrimaryLevelID: 1}
		event.PlanRef.StrategyID = "2"
		event.RecordRef.DimensionIdentityDigest, event.RecordRef.RecordID, event.RecordRef.SourceTime = series, "point", 60
		event.LevelResults = []contract.LevelResultV1{{LevelID: 1}, {LevelID: 9}}
		r.StateResults = []execution.StateEvaluation{{Events: []contract.TriggerEventV1{event}}}
		f.evaluated(execution.EvaluationRequest{}, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{r}})
		var err error
		if series == "b" {
			err = errors.New("partial broker acknowledgement is unknown")
		}
		f.output([]contract.TriggerEventV1{event}, err)
	}
	if p.selected != 2 || *p.outcomes.PrimaryAbnormal.Value != 2 || p.produced != 2 || p.acked != 1 || !p.ackUnknown {
		t.Fatalf("record/message units: %+v", p)
	}
	if *p.levels[1].Selected.Value != 2 || *p.levels[9].Selected.Value != 2 || *p.levels[1].Primary.Value != 2 || *p.levels[9].SiblingDiagnostic.Value != 2 || *p.levels[9].SuppressedAbnormal.Value != 2 {
		t.Fatalf("Level units: %+v %+v", p.levels[1], p.levels[9])
	}
}

func TestPhaseTwoReceiptZeroQueryPrefixBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	request := execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", EvaluationTime: 100}}, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "a", OwnerEpoch: 1, LeaseToken: "token"}}
	// Only successful first Begin CAS and zero actual calls can create a prefix.
	for _, mode := range []string{"clean", "cas_failed", "prior", "actual_query", "terminal", "owner", "expired", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			e := &phaseTwoFinalEmitter{}
			e.manifest.Limits.MaxEntries = 1
			e.manifest.Limits.MaxAgeSeconds = 10
			f := &phaseTwoReceiptFacts{request: request, beginKnown: true}
			switch mode {
			case "cas_failed":
				f.beginKnown = false
			case "prior":
				f.priorUnfinished = true
			case "actual_query":
				f.calls = 1
			case "capacity":
				e.manifest.Limits.MaxEntries = 0
			}
			e.finishZeroQueryPrefix(f, now, mode == "terminal")
			next := request
			at := now.Add(time.Second)
			if mode == "owner" {
				next.OwnerFence.OwnerEpoch++
			}
			if mode == "expired" {
				at = now.Add(11 * time.Second)
			}
			started := e.takeZeroQueryPrefix(next, at)
			if (!started.IsZero()) != (mode == "clean") {
				t.Fatal(mode, started)
			}
			if len(e.zeroPrefixes) != 0 {
				t.Fatal("entry not consumed/cleared")
			}
			if mode == "clean" {
				f.prefixKnown = true
				f.prefixStarted = started
				f.priorUnfinished = true
				e.finishZeroQueryPrefix(f, now.Add(9*time.Second), false)
				if !e.takeZeroQueryPrefix(request, now.Add(11*time.Second)).IsZero() {
					t.Fatal("retry reset age")
				}
			}
		})
	}
}
func TestPhaseTwoReceiptActualNormalAndFullEmpty(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "query_failure"} {
		t.Run(mode, func(t *testing.T) { testPhaseTwoShadowActualThresholdACKAndIsolation(t, true, mode) })
	}
}
