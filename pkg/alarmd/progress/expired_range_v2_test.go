package progress

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func distanceRangeFixture(t *testing.T) execution.ExpiredRangeProjectionV1 {
	t.Helper()
	p := rangeFixture(t)
	p.ReplayAgeMillis = 600000
	p.First.KeepUntilUnixMilli = p.First.EarliestQueryDeadlineUnixMilli + 660000
	p.Last.KeepUntilUnixMilli = p.Last.EarliestQueryDeadlineUnixMilli + 660000
	p.JudgedAtMillis = 400000
	p.EligibilityV2 = &execution.ExpiredRangeEligibilityV2{Reason: execution.RangeDistanceExpired, MaxReplaySlots: 3, DistanceHead: 360}
	p, err := execution.SealExpiredRange(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExpiredRangeV2WireRejectsVersionConfusion(t *testing.T) {
	p := distanceRangeFixture(t)
	raw := mustEncode(t, execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 60, UnfinishedRange: &p})
	if !bytes.Contains(raw, []byte(schemaRangeV2)) {
		t.Fatal("V2 proof lacks explicit schema")
	}
	decoded, err := decode(raw)
	if err != nil || !decoded.UnfinishedRange.Equal(p) {
		t.Fatalf("roundtrip: %v", err)
	}
	for name, bad := range map[string][]byte{
		"v1 schema":           bytes.Replace(raw, []byte(schemaRangeV2), []byte(schemaRangeV1), 1),
		"ordinary schema":     bytes.Replace(raw, []byte(schemaRangeV2), []byte(schemaV2), 1),
		"missing eligibility": []byte(strings.Replace(string(raw), `"EligibilityV2":`, `"unknown":`, 1)),
		"unknown":             []byte(strings.Replace(string(raw), `"MaxReplaySlots":3`, `"MaxReplaySlots":3,"other":1`, 1)),
		"duplicate":           []byte(strings.Replace(string(raw), `"Reason":"DISTANCE_EXPIRED"`, `"Reason":"DISTANCE_EXPIRED","Reason":"DISTANCE_EXPIRED"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(bad); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}

func TestExpiredRangeV2EligibilityAndClone(t *testing.T) {
	p := distanceRangeFixture(t)
	for name, change := range map[string]func(*execution.ExpiredRangeProjectionV1){
		"recoverable": func(p *execution.ExpiredRangeProjectionV1) { p.EligibilityV2.MaxReplaySlots = 4 },
		"future head": func(p *execution.ExpiredRangeProjectionV1) { p.EligibilityV2.DistanceHead = 420 },
		"off grid":    func(p *execution.ExpiredRangeProjectionV1) { p.EligibilityV2.DistanceHead = 359 },
		"age mixed": func(p *execution.ExpiredRangeProjectionV1) {
			p.JudgedAtMillis = p.First.EarliestQueryDeadlineUnixMilli + p.ReplayAgeMillis
		},
		"not deadline": func(p *execution.ExpiredRangeProjectionV1) {
			p.JudgedAtMillis = p.Last.EarliestQueryDeadlineUnixMilli - 1
		},
		"bad reason": func(p *execution.ExpiredRangeProjectionV1) { p.EligibilityV2.Reason = "NORMAL" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := p.Clone()
			change(&copy)
			if _, err := execution.SealExpiredRange(copy); err == nil {
				t.Fatal("unsafe boundary sealed")
			}
			if err := p.Validate(); err != nil {
				t.Fatal("clone altered caller", err)
			}
		})
	}
}

func TestExpiredRangeV1ProofBytesRemainOriginal(t *testing.T) {
	p := rangeFixture(t)
	old := struct {
		Schedule           execution.FrozenQueryGroupSchedule
		First              execution.UnfinishedSlotProjection
		Last               execution.UnfinishedSlotProjection
		Next               execution.EvaluationTime
		Count              uint32
		QueryReserveMillis int64
		ReplayAgeMillis    int64
		JudgedAtMillis     int64
		Digest             string
	}{p.Schedule, p.First, p.Last, p.Next, p.Count, p.QueryReserveMillis, p.ReplayAgeMillis, p.JudgedAtMillis, ""}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-expired-range-projection-v1", old)
	if err != nil || digest != p.Digest {
		t.Fatalf("V1 digest changed: %v", err)
	}
	old.Digest = digest
	want, _ := json.Marshal(old)
	got, _ := json.Marshal(p)
	if !bytes.Equal(want, got) {
		t.Fatal("V1 wire changed")
	}
}

func TestExpiredRangeV2CommitsDistinctReasonWithoutFull(t *testing.T) {
	p := distanceRangeFixture(t)
	fake := &controlFake{value: mustEncode(t, execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "q"}, NextSlot: 60})}
	store := mustStore(t, fake)
	request := execution.ExpiredRangeRequest{Projection: p, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}}
	request.OwnerFence.QueryGroup = "q"
	if r, e := store.BeginRange(context.Background(), request); e != nil || r.Status != execution.ProgressCommitted {
		t.Fatalf("begin %v %v", r, e)
	}
	if r, e := store.CommitRange(context.Background(), request); e != nil || r.Status != execution.ProgressCommitted {
		t.Fatalf("commit %v %v", r, e)
	}
	got, e := decode(fake.value)
	if e != nil {
		t.Fatal(e)
	}
	if got.LastCompletionKind != execution.CompletionGapSkipped || got.CurrentOrRecentGap.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) || got.LastFullSlot != 0 || got.NextSlot != 240 || got.UnfinishedRange != nil {
		t.Fatalf("wrong terminal: %+v", got)
	}
}
