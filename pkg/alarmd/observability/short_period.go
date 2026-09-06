package observability

import "math"

// QueryTimingFacts describe frozen/query-attempt timing, not HTTP or Slot counts.
type QueryTimingFacts struct {
	IntervalSeconds              int64 `json:"interval_seconds"`
	CompletionOffsetSeconds      int64 `json:"completion_offset_seconds"`
	ReadyAtUnixMilli             int64 `json:"ready_at_unix_milli"`
	FrozenQueryDeadlineUnixMilli int64 `json:"frozen_query_deadline_unix_milli"`
	CompletionDeadlineUnixMilli  int64 `json:"completion_deadline_unix_milli"`
	QueryDeadlineUnixMilli       int64 `json:"query_deadline_unix_milli"`
	RecoveryBudgetMilli          int64 `json:"recovery_budget_milli,omitempty"`
}

// ShortPeriodCompletionFacts exists only after an acknowledged Progress commit.
type ShortPeriodCompletionFacts struct {
	Cohort         string  `json:"cohort"`
	CompletionKind string  `json:"completion_kind"`
	LagSeconds     float64 `json:"lag_seconds"`
}

func normalizeTimingFacts(o Observation) *QueryTimingFacts {
	f := o.QueryTiming
	if f == nil || o.Component != ComponentAccess || o.Stage != StageQueryBudgetResolved ||
		(f.IntervalSeconds != 10 && f.IntervalSeconds != 15) || f.CompletionOffsetSeconds <= 0 ||
		f.ReadyAtUnixMilli <= 0 || f.FrozenQueryDeadlineUnixMilli <= 0 || f.CompletionDeadlineUnixMilli <= f.FrozenQueryDeadlineUnixMilli ||
		f.QueryDeadlineUnixMilli <= 0 || f.RecoveryBudgetMilli < 0 {
		return nil
	}
	copy := *f
	return &copy
}

func normalizeShortPeriodCompletion(o Observation) *ShortPeriodCompletionFacts {
	f := o.ShortPeriodCompletion
	if f == nil || o.Component != ComponentScheduler || o.Stage != StageSlotCompleted ||
		(f.Cohort != "10s" && f.Cohort != "15s") || f.LagSeconds < 0 || math.IsNaN(f.LagSeconds) || math.IsInf(f.LagSeconds, 0) || o.Err != nil {
		return nil
	}
	switch f.CompletionKind {
	case "FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "COMPLETED_WITH_PARTIAL_GAP", "COMPLETED_WITH_UNAVAILABLE", "COMPLETED_WITH_TERMINAL", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE":
	default:
		return nil
	}
	switch o.Operation {
	case OperationNormal, OperationRetry, OperationReplay, OperationProbe:
	default:
		return nil
	}
	copy := *f
	return &copy
}
