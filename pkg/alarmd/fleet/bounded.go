// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"

// Everything below exists for one boundary: a metric label. The JSON API keeps
// reporting whatever the pipeline actually said, because a response has no
// cardinality budget and a reader deserves the real value. A label does have a
// budget, and it is spent by whoever adds the next classification upstream --
// which is not a decision the metric family should make on its own.
//
// So the enums here are closed and unknown values land in a visible bucket. A
// new classification then shows up as a growing OTHER rather than as a silently
// widening metric, and adding it to the label set stays a deliberate act.
const (
	// LabelOther collects classifications this build does not know about.
	LabelOther = "OTHER"
)

// AnomalyKinds is the closed set of kinds this build produces.
//
// It is one list with two readers on purpose: the metric label set below, and
// the page's own wording for each kind. Two lists drift, and the way they drift
// is silent -- a kind the page has no word for renders as whichever word the
// fallback happens to be, which reads as an ordinary row of a familiar kind.
// That is worse than showing the raw name, so the page is checked against this.
var AnomalyKinds = []string{KindDegradedRun, KindBlockedRun, KindOverdueWake, KindQueryCooldown, KindSkippedSpan, KindNoData}

// SinceSources is the closed set of start-time provenances this build produces.
//
// It exists for the same reason AnomalyKinds does, and it is the list that was
// missing when the field had only one value: the page maps the value to wording,
// and an unmapped value falls back to printing the raw name beside a timestamp
// whose meaning that name was supposed to explain. Three of these are not a
// start time at all, so the fallback is not a cosmetic loss.
var SinceSources = []SinceSource{
	SinceBusinessState,
	SinceSnapshotContinuity,
	SinceProcessStart,
	SinceRestoredLastFull,
	SinceRestoredAtRestart,
	SinceRefusedFuture,
}

// RestoredSinceSources are the provenances that mean the object was rebuilt
// from what was written down rather than watched going wrong.
//
// The distinction is not cosmetic on the page. What gets persisted is the
// completion kind; the cause and the reason below it are not, so a restored
// object carries no cause -- and a blank cause reads as "there is no cause"
// when the truth is "the cause was not kept". Those tell a reader to do
// opposite things, and after every rollout the second one is most of the list.
var RestoredSinceSources = []SinceSource{
	SinceRestoredLastFull,
	SinceRestoredAtRestart,
}

// BoundedSinceSources are the provenances whose timestamp is a bound rather
// than a measurement: the object started before this, or after it, and the
// duration rendered beside it is not how long anything has been going on.
//
// It is a different set from RestoredSinceSources and has to be, which is why
// it is written out rather than derived from it. A pre-existing object was not
// restored from anything -- this process simply started with it already wrong --
// and its duration is a lower bound all the same.
//
// The list exists because the roll-up above the table had no notion of it. Each
// row states its own direction, and then a sentence over them read the largest
// of those numbers back as a moment: "最新的一个是 2 小时 2 分前开始的", on a
// population whose rows say in the next column that the moment it started was
// never recorded. The bound is per row, and a summary that drops it re-asserts
// as fact the one thing the row was careful not to claim.
var BoundedSinceSources = []SinceSource{
	SinceProcessStart,
	SinceRestoredAtRestart,
	SinceRestoredLastFull,
	SinceRefusedFuture,
}

// Bounded reports whether this provenance gives a bound instead of a start.
func (source SinceSource) Bounded() bool {
	for _, bounded := range BoundedSinceSources {
		if source == bounded {
			return true
		}
	}
	return false
}

// MetricKind maps an anomaly kind onto the closed label set.
func MetricKind(kind string) string {
	for _, known := range AnomalyKinds {
		if kind == known {
			return kind
		}
	}
	return LabelOther
}

// MetricGapKind maps a gap onto the closed label set.
func MetricGapKind(kind GapKind) string {
	switch kind {
	case GapDenominatorUnavailable, GapReplicaMissing, GapSnapshotStale, GapListTruncated,
		GapOwnershipShortfall, GapCoverageInconsistent, GapNoReplicas, GapUndetermined,
		GapRegistryUnavailable:
		return string(kind)
	default:
		return LabelOther
	}
}

// MetricFailureCategory maps a query failure onto the closed label set the
// pipeline already publishes, which carries its own "other" bucket.
func MetricFailureCategory(category string) string {
	switch category {
	case observability.QueryFailureCategorySourceBackend,
		observability.QueryFailureCategorySeriesIdentity,
		observability.QueryFailureCategoryBudget,
		observability.QueryFailureCategoryCompletionContract,
		observability.QueryFailureCategoryNamedInput,
		observability.QueryFailureCategoryProviderTransport,
		observability.QueryFailureCategoryAdmission,
		observability.QueryFailureCategoryEvaluation,
		observability.QueryFailureCategoryOther:
		return category
	default:
		return observability.QueryFailureCategoryOther
	}
}

// ResultContractRefusals is the closed vocabulary of the result contract's
// refusal codes, as this package needs to see them.
//
// The contract refuses a mutation this deployment itself produced, so the whole
// family is ours by construction: an external condition may be what the
// evaluation met, but writing a result that contradicts itself in the face of
// it is this code. Classifying any of them as external would let a defect
// escape attribution as soon as someone finds an outside trigger for it.
//
// It is a copy: the vocabulary is declared in execution, which this package
// does not import, and the alternative -- importing it for a list of strings --
// would tie the fleet view to the execution contract for nothing else.
// TestEveryResultContractCodeIsClassified compares this list with that
// vocabulary in both directions, so the copy cannot fall behind the original
// without a test saying so. A copy nothing compares is what this file already
// learned not to keep.
var ResultContractRefusals = []string{
	"OUTCOME_PRIMARY_RECORD_MISSING",
	"OUTCOME_NOT_A_SELECTED_LEVEL",
	"OUTCOME_DUPLICATE",
	"OUTCOME_MISSING_FOR_LEVEL",
	"OUTCOME_IDENTITY_INCOMPLETE",
	"OUTCOME_KIND_INVALID",
	"OUTCOME_RETRYABLE_SERIES_NOT_UNKNOWN",
	"OUTCOME_INVALID_SERIES_NOT_TERMINAL",
	"OUTCOME_BUSINESS_UNDER_ACTIVE_GUARD",
	"OUTCOME_UNKNOWN_DROPS_GUARD_REASON",
	"OUTCOME_EFFECTIVE_TIME_FACT_MISSING",
	"OUTCOME_EFFECTIVE_TIME_UNKNOWN_MISSED",
	"OUTCOME_TERMINAL_DEPENDENCY_MISSED",
	"OUTCOME_UNAVAILABLE_DEPENDENCY_BUSINESS",
	"PROOF_ON_FULL_OUTCOME",
	"OUTCOME_PARTIAL_NORMAL_OR_RECOVERY",
	"PROOF_ON_NON_ABNORMAL_PARTIAL",
	"PROOF_CAPABILITY_MISSING",
	"PROOF_DUPLICATE",
	"PROOF_DOES_NOT_CLOSE_EVIDENCE",
	"PROOF_MISSING_FOR_PARTIAL_INPUT",
	"STATE_LOADED_VIEW_MISSING",
	"LOCALIZED_TERMINAL_NOT_TERMINAL",
	"LOCALIZED_TERMINAL_REASON_DIFFERS",
	"LOCALIZED_QUALITY_NOT_UNKNOWN",
	"LOCALIZED_QUALITY_REASON_DIFFERS",
	"STATE_ANCHOR_UNJUSTIFIED",
	"STATE_FACT_OUTCOME_MISSING",
	"STATE_FACT_CONTRADICTS_OUTCOME",
	"STATE_FACT_DUPLICATE",
	"STATE_PARTIAL_ABNORMAL_DROPS_PROVENANCE",
	"STATE_FACT_MISSING_FOR_OUTCOME",
	"HISTORY_RETENTION_BOUND_MISSING",
	"HISTORY_LOADED_POINT_CHANGED",
	"HISTORY_LOADED_POINT_CHANGED_OR_INVENTED",
	"HISTORY_SNAPSHOT_INCOMPLETE",
	"GUARD_MISSING_FOR_PARTIAL_ABNORMAL",
	"GUARD_MISSING_FOR_DEGRADED_OUTCOME",
	"GUARD_MISSING_PRE_EVENT",
	"EVENT_EFFECTIVE_TIME_FACT_MISSING",
	"EVENT_DUPLICATE",
	"EVENT_KIND_MISMATCH",
	"EVENT_LEVEL_RESULT_CONTRADICTS_OUTCOME",
	"EVENT_OMITS_SIBLING_OUTCOME",
	"EVENT_ENVELOPE_COUNT_INVALID",
	"EVENT_MISSING_FOR_BUSINESS_OUTCOME",
	"EVENT_HELD_ON_NON_RECOVERY_OUTCOME",
	"EVENT_HELD_DISAGREES_ACROSS_LEVELS",
}
