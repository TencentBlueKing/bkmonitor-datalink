package execution

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A range proof is one bounded control value, not a per-Slot history. This
// internal wire ceiling is independent of logical span and query record limits.
const MaxExpiredRangeProjectionBytes = 1 << 20

var ErrExpiredRangeProofTooLarge = errors.New("alarmd execution: expired range proof exceeds control value bound")

// ExpiredRangeProjectionV1 is the single recoverable Progress pending value.
// Schedule contains cadence facts, never compiled Plans or execution authority.
type ExpiredRangeProjectionV1 struct {
	Schedule           FrozenQueryGroupSchedule
	First              UnfinishedSlotProjection
	Last               UnfinishedSlotProjection
	Next               EvaluationTime
	Count              uint32
	QueryReserveMillis int64
	ReplayAgeMillis    int64
	JudgedAtMillis     int64
	Digest             string
}

func (p ExpiredRangeProjectionV1) Clone() ExpiredRangeProjectionV1 {
	p.Schedule.Plans = append([]FrozenPlanSchedule(nil), p.Schedule.Plans...)
	if p.Schedule.Segment.End != nil {
		end := *p.Schedule.Segment.End
		p.Schedule.Segment.End = &end
	}
	p.First.DuePlanTargets = p.First.DuePlanTargets.Clone()
	p.Last.DuePlanTargets = p.Last.DuePlanTargets.Clone()
	return p
}

func (p ExpiredRangeProjectionV1) Equal(other ExpiredRangeProjectionV1) bool {
	return reflect.DeepEqual(p, other)
}

func (p ExpiredRangeProjectionV1) Validate() error {
	if err := p.validateFacts(); err != nil {
		return err
	}
	got := p.Digest
	p.Digest = ""
	want, err := contract.DeriveCanonicalDigestV2("alarmd-expired-range-projection-v1", p)
	if err != nil {
		return err
	}
	if got == "" || got != want {
		return errors.New("alarmd execution: expired range digest mismatch")
	}
	return nil
}

func SealExpiredRange(p ExpiredRangeProjectionV1) (ExpiredRangeProjectionV1, error) {
	p = p.Clone()
	p.Digest = ""
	if err := p.validateFacts(); err != nil {
		return ExpiredRangeProjectionV1{}, err
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-expired-range-projection-v1", p)
	p.Digest = digest
	if err == nil {
		raw, encodeErr := json.Marshal(p)
		if encodeErr != nil {
			return ExpiredRangeProjectionV1{}, encodeErr
		}
		if len(raw) > MaxExpiredRangeProjectionBytes {
			return ExpiredRangeProjectionV1{}, ErrExpiredRangeProofTooLarge
		}
	}
	return p, err
}

func (p ExpiredRangeProjectionV1) validateFacts() error {
	bad := func() error { return errors.New("alarmd execution: invalid expired range proof") }
	if p.Count < 2 || p.QueryReserveMillis < 0 || p.ReplayAgeMillis <= 0 || p.JudgedAtMillis <= 0 {
		return bad()
	}
	if err := p.Schedule.Validate(); err != nil {
		return err
	}
	if err := p.First.Validate(); err != nil {
		return err
	}
	if err := p.Last.Validate(); err != nil {
		return err
	}
	first, last := p.First.Contract.Slot.EvaluationTime, p.Last.Contract.Slot.EvaluationTime
	if first > last || last > EvaluationTime(math.MaxInt64/1000) || p.Next <= last {
		return bad()
	}
	spec := p.Schedule.Plans[0].Spec
	i := spec.EvaluationIntervalSeconds
	if (int64(last)-int64(first))%i != 0 || uint64((int64(last)-int64(first))/i)+1 != uint64(p.Count) {
		return bad()
	}
	for _, plan := range p.Schedule.Plans {
		if plan.Spec.EvaluationIntervalSeconds != i || plan.Spec.Alignment != spec.Alignment {
			return bad()
		}
	}
	if len(p.First.DuePlanTargets.Plans) != len(p.Schedule.Plans) || len(p.Last.DuePlanTargets.Plans) != len(p.Schedule.Plans) {
		return bad()
	}
	for _, projection := range []UnfinishedSlotProjection{p.First, p.Last} {
		targets := make(map[PlanIdentity]struct{}, len(projection.DuePlanTargets.Plans))
		for _, target := range projection.DuePlanTargets.Plans {
			targets[target] = struct{}{}
		}
		ref := projection.Contract
		segment := p.Schedule.Segment
		if !segment.Contains(ref.Slot.EvaluationTime) || ref.Slot.QueryGroup != segment.QueryGroup ||
			ref.SnapshotRevision != segment.Publication.SnapshotRevision || ref.QueryRevision != segment.QueryRevision ||
			ref.ScheduleRevision != segment.ScheduleRevision || ref.ScheduleSegmentStart != segment.Start {
			return bad()
		}
		deadline := int64(math.MaxInt64)
		for _, plan := range p.Schedule.Plans {
			if !plan.Spec.IsAligned(ref.Slot.EvaluationTime) {
				return bad()
			}
			if _, found := targets[plan.Identity]; !found {
				return bad()
			}
			d, ok := plan.Spec.CompletionDeadlineUnixMilli(ref.Slot.EvaluationTime)
			if !ok || d <= p.QueryReserveMillis {
				return bad()
			}
			d -= p.QueryReserveMillis
			if d < deadline {
				deadline = d
			}
		}
		if deadline != projection.EarliestQueryDeadlineUnixMilli || deadline > math.MaxInt64-p.ReplayAgeMillis ||
			deadline+p.ReplayAgeMillis > p.JudgedAtMillis || projection.KeepUntilUnixMilli <= deadline+p.ReplayAgeMillis {
			return bad()
		}
	}
	// Within this Segment no legal Slot may be skipped. At a boundary the
	// caller additionally proves Next using the live authoritative timeline.
	if next, ok := p.Schedule.NextSlotAfter(last); ok && next != p.Next {
		return bad()
	}
	return nil
}

type ExpiredRangeRequest struct {
	OwnerFence OwnerFence
	Projection ExpiredRangeProjectionV1
}

func (r ExpiredRangeRequest) Validate() error {
	if err := r.Projection.Validate(); err != nil {
		return err
	}
	return r.OwnerFence.Validate(r.Projection.Last.Contract)
}

type ExpiredRangeResult struct {
	Status           ProgressCommitStatus
	ReasonCode       ReasonCode
	AlreadyCommitted bool
}

// ExpiredRangeStore extends only range-aware coordinators; existing single
// Slot ports and byte representations remain unchanged.
type ExpiredRangeStore interface {
	BeginRange(context.Context, ExpiredRangeRequest) (ExpiredRangeResult, error)
	CommitRange(context.Context, ExpiredRangeRequest) (ExpiredRangeResult, error)
}
