package observability

import "testing"

// The cohort is about the completion deadline, not about the interval being
// small. Every interval whose deadline is thirty seconds belongs, and 30 is the
// one with the least headroom of the three: 10 and 15 carry an explicit
// thirty-second offset so their deadline spans two or three intervals, while 30
// reaches thirty by the offset defaulting to the interval, so its deadline is
// exactly one interval wide.
//
// It was excluded, and that exclusion was invisible: the aggregate execution
// histogram has no period dimension and the cohort histogram did not cover it,
// so executions over thirty seconds could not be attributed to it or cleared of
// it. Two separately drawn observation boundaries met at the same interval and
// it fell through the gap between them.
func TestShortPeriodCohortCoversEveryThirtySecondDeadline(t *testing.T) {
	for _, cohort := range []string{"10s", "15s", "30s"} {
		if !IsShortPeriodCohort(cohort) {
			t.Errorf("cohort %q is not recognized", cohort)
		}
	}
	for _, cohort := range []string{"", "5s", "60s", "300s", "30"} {
		if IsShortPeriodCohort(cohort) {
			t.Errorf("cohort %q is recognized but has no thirty-second deadline", cohort)
		}
	}
}

// The set is declared once. It used to be written out separately in the
// producer, this normalizer and the runtime, which is how one member came to be
// missing from all three without anything noticing.
func TestShortPeriodCohortsAndTheirCheckAgree(t *testing.T) {
	if len(ShortPeriodCohorts) != 3 {
		t.Fatalf("cohorts=%v, want the three thirty-second-deadline intervals", ShortPeriodCohorts)
	}
	for _, cohort := range ShortPeriodCohorts {
		if !IsShortPeriodCohort(cohort) {
			t.Errorf("declared cohort %q is rejected by its own check", cohort)
		}
	}
}

// The sites that decide by interval and the sites that decide by cohort label
// must agree about which Slots are short. They did not: adding 30 to the cohort
// put it in the lag and duration histograms while the query budget telemetry,
// gated separately on the interval pair, emitted nothing for it - present on
// one side of a cross-check and absent on the other. Both now decide by the
// deadline.
func TestQueryBudgetFactsCoverTheSameSetAsTheCohort(t *testing.T) {
	facts := func(interval int64) *QueryTimingFacts {
		return &QueryTimingFacts{
			IntervalSeconds: interval, CompletionOffsetSeconds: ShortPeriodCompletionOffsetSeconds,
			ReadyAtUnixMilli: 1_700_000_010_000, FrozenQueryDeadlineUnixMilli: 1_700_000_020_000,
			CompletionDeadlineUnixMilli: 1_700_000_030_000, QueryDeadlineUnixMilli: 1_700_000_020_000,
		}
	}
	for _, interval := range []int64{10, 15, 30} {
		observation := Observation{Component: ComponentAccess, Stage: StageQueryBudgetResolved, QueryTiming: facts(interval)}
		if normalizeTimingFacts(observation) == nil {
			t.Errorf("interval %ds is in the cohort but reports no query budget", interval)
		}
	}
	for _, interval := range []int64{60, 300} {
		observation := Observation{Component: ComponentAccess, Stage: StageQueryBudgetResolved, QueryTiming: facts(interval)}
		if normalizeTimingFacts(observation) != nil {
			t.Errorf("interval %ds is not in the cohort but reports a query budget", interval)
		}
	}
}
