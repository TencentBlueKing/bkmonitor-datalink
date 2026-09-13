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
var AnomalyKinds = []string{KindDegradedRun, KindBlockedRun, KindOverdueWake, KindQueryCooldown}

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
	SinceRestoredLastFull,
	SinceRestoredAtRestart,
	SinceRefusedFuture,
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
