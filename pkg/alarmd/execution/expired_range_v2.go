package execution

import (
	"errors"
	"math"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	RangeAgeExpired      = "AGE_EXPIRED"
	RangeDistanceExpired = "DISTANCE_EXPIRED"
)

// ExpiredRangeEligibilityV2 is an explicit new proof branch, not an amendment
// to V1. DistanceHead is a real grid point in the supplied homogeneous Schedule.
// It witnesses at least MaxReplaySlots successors without a per-slot array.
type ExpiredRangeEligibilityV2 struct {
	Reason         string
	MaxReplaySlots uint32
	DistanceHead   EvaluationTime
}

func (p ExpiredRangeProjectionV1) digestDomain() string {
	if p.EligibilityV2 != nil {
		return "alarmd-expired-range-projection-v2"
	}
	return "alarmd-expired-range-projection-v1"
}

func (p ExpiredRangeProjectionV1) CompletionKind() CompletionKind {
	if p.EligibilityV2 != nil && p.EligibilityV2.Reason == RangeDistanceExpired {
		return CompletionGapSkipped
	}
	return CompletionSnapshotUnavailable
}

func (p ExpiredRangeProjectionV1) CompletionReason() ReasonCode {
	if p.CompletionKind() == CompletionGapSkipped {
		return ReasonCode(contract.ReasonGapSkipped)
	}
	return ReasonCode(contract.ReasonSnapshotUnavailable)
}

func (p ExpiredRangeProjectionV1) validateEligibility(s UnfinishedSlotProjection) error {
	bad := errors.New("alarmd execution: invalid range eligibility proof")
	deadline := s.EarliestQueryDeadlineUnixMilli
	if p.EligibilityV2 == nil {
		if deadline+p.ReplayAgeMillis > p.JudgedAtMillis {
			return bad
		}
		return nil
	}
	e := p.EligibilityV2
	switch e.Reason {
	case RangeAgeExpired:
		if e.MaxReplaySlots != 0 || e.DistanceHead != 0 || deadline+p.ReplayAgeMillis > p.JudgedAtMillis {
			return bad
		}
	case RangeDistanceExpired:
		if e.MaxReplaySlots == 0 || deadline > p.JudgedAtMillis || deadline+p.ReplayAgeMillis <= p.JudgedAtMillis {
			return bad
		}
		head := int64(e.DistanceHead)
		if head <= 0 || head > math.MaxInt64/1000 || head*1000 > p.JudgedAtMillis || !p.Schedule.Segment.Contains(e.DistanceHead) {
			return bad
		}
		spec := p.Schedule.Plans[0].Spec
		if !spec.IsAligned(e.DistanceHead) || e.DistanceHead < p.Last.Contract.Slot.EvaluationTime {
			return bad
		}
		if uint64((head-int64(p.Last.Contract.Slot.EvaluationTime))/spec.EvaluationIntervalSeconds) < uint64(e.MaxReplaySlots) {
			return bad
		}
	default:
		return bad
	}
	return nil
}
