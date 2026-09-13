// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// The anomaly column answers "what did this deployment fail to evaluate". That
// is not the same question as "is this deployment well", and the two were being
// read off one number.
//
// A live read had 99 objects in that column. Most were an algorithm waiting for
// history it does not have yet, a strategy edited underneath a round, or a
// backend that timed out. None of those get better with more replicas, more
// memory or a different design, and none of them are evidence against the
// deployment -- but every one of them made the verdict DEGRADED, so the verdict
// had been DEGRADED continuously for reasons alarmd cannot act on. A signal
// that is always on is not a signal.
//
// So the column is split by one question: would more capacity, or a different
// design, have prevented this? Only the objects where the answer is yes bear on
// whether the deployment is well; the rest are real work for someone else.
type Attribution string

const (
	// AttributionOurs is what capacity or design could have prevented: budgets
	// spent, queues that did not drain, rounds that stopped ending, state this
	// deployment could not read or write, objects it never got to.
	AttributionOurs Attribution = "OURS"
	// AttributionExternal is everything the deployment carried out correctly and
	// still could not produce a result for: the data is not there, the backend
	// did not answer, the strategy says something this build cannot evaluate.
	AttributionExternal Attribution = "EXTERNAL"
)

// externalReasons are the reason codes that are not evidence against this
// deployment. Everything in the catalogue that is not listed here counts
// against it -- see attributionOf for why that is the safe direction.
//
// Grouped by who acts on it, because that is what the split is for.
var externalReasons = map[string]bool{
	// The data does not reach the window the algorithm needs. Nothing about
	// this deployment changes that.
	"HISTORY_GAPPED":  true,
	"HISTORY_WARMING": true,
	"GAP_SKIPPED":     true,
	"QUERY_NOT_READY": true,

	// The backend was asked correctly and did not answer, or answered that the
	// thing being asked for does not exist.
	"QUERY_TIMEOUT":        true,
	"QUERY_UNAVAILABLE":    true,
	"QUERY_PARTIAL":        true,
	"PROVIDER_UNAVAILABLE": true,
	"LATE_OUT_OF_WINDOW":   true,

	// The strategy's own configuration, or a change to it. CONFIG_DRIFT is the
	// strategy being edited between two reads of one round -- correct behaviour
	// on both sides, and it clears itself.
	"CONFIG_DRIFT":            true,
	"EFFECTIVE_TIME_INACTIVE": true,
	"EFFECTIVE_TIME_UNKNOWN":  true,

	// The definition handed to this deployment cannot be evaluated as written.
	// Someone has to change the strategy, or this build has to grow support --
	// neither is a capacity or design fault in the running deployment.
	"ALGORITHM_UNSUPPORTED":                 true,
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": true,
	"REQUIRED_FEATURE_UNSUPPORTED":          true,
	"SCHEMA_MAJOR_UNSUPPORTED":              true,
	"PLAN_INVALID":                          true,
	"PLAN_DUPLICATE_LEVEL_ID":               true,
	"PLAN_SET_CONFLICT":                     true,
	"LEVEL_INVALID":                         true,
	"SELECTOR_INVALID":                      true,
	"SELECTOR_ORDINAL_INVALID":              true,
	"REQUIRED_VALUE_MISSING":                true,
	"REQUIRED_VALUE_TYPE_MISMATCH":          true,
	"REQUIRED_VALUE_NORMALIZATION_FAILED":   true,
	"TIME_INVALID":                          true,
	"TENANT_INVALID":                        true,
	"MALFORMED_JSON":                        true,
	"PAYLOAD_DIGEST_MISMATCH":               true,
	"RECORD_INVALID":                        true,
	"RECORD_IDENTITY_CONFLICT":              true,
}

// ourReasons are the codes that do count against the deployment. Listed rather
// than left to the default so that a code added to the catalogue fails the
// completeness test instead of quietly picking a side.
var ourReasons = map[string]bool{
	// Budgets: this deployment ran out of something it allocates itself.
	"EXECUTION_BUDGET_EXHAUSTED": true,
	"SLOT_BUDGET_EXCEEDED":       true,
	"LEVEL_BUDGET_EXCEEDED":      true,
	"PLAN_BUDGET_EXCEEDED":       true,
	"STATE_BUDGET_EXCEEDED":      true,
	"VALIDATION_BUDGET_EXCEEDED": true,
	"MESSAGE_BUDGET_EXCEEDED":    true,
	"READINESS_BUDGET_INVALID":   true,
	"RECORD_TOO_LARGE":           true,
	"RESOURCE_HARD_STOP":         true,

	// Our own stores and infrastructure.
	"REDIS_UNAVAILABLE":        true,
	"KAFKA_UNAVAILABLE":        true,
	"STATE_CORRUPT":            true,
	"STATE_WRITE_RETRYABLE":    true,
	"STATE_SCHEMA_UNSUPPORTED": true,
	"PROJECTION_INVALID":       true,
	"PROGRESS_BEGIN_FAILED":    true,
	"PROGRESS_BEGIN_REJECTED":  true,
	"OUTPUT_ACK_UNKNOWN":       true,
	"AUDIT_DROP":               true,

	// Our control plane did not give the runner something to run. A live read
	// found twelve objects here whose strategies had been retired days
	// earlier: the disposition was known inside the system and never reached
	// the runner, so it retried a dead object every thirty seconds for forty
	// hours. That is a design gap in this deployment, not the strategy's doing.
	"BLOCKED_EXACT_SET_UNAVAILABLE": true,
	"SLOT_SOURCE_RETRY":             true,
	"SNAPSHOT_UNAVAILABLE":          true,
	"SNAPSHOT_RETRY_PENDING":        true,
	"ACTIVATION_READ_FAILED":        true,
}

// attributionOf decides which side one anomaly falls on.
//
// The order is deliberate: the strongest evidence about this deployment is
// checked first, because an object that has stopped progressing is ours
// whatever its last reason code said. A stalled object's last recorded reason
// is often the external thing that happened before it got stuck.
func attributionOf(anomaly Anomaly) Attribution {
	// Rounds have stopped ending. Nothing outside this deployment can produce
	// that, and nothing outside it will end them.
	if anomaly.Stalled {
		return AttributionOurs
	}
	// Never reached at all. Whatever the object would have done, not getting to
	// it is this deployment's.
	if anomaly.Kind == KindOverdueWake {
		return AttributionOurs
	}
	// The most specific reason available wins, innermost first: the reason under
	// the cause says more than the completion kind above it.
	for _, code := range []string{anomaly.CauseReason, string(anomaly.Cause), anomaly.ReasonCode} {
		if code == "" {
			continue
		}
		if externalReasons[code] {
			return AttributionExternal
		}
		if ourReasons[code] {
			return AttributionOurs
		}
	}
	if anomaly.Failure != nil {
		if externalReasons[anomaly.Failure.Code] {
			return AttributionExternal
		}
		if ourReasons[anomaly.Failure.Code] {
			return AttributionOurs
		}
	}
	// Unclassified counts against the deployment, and that is the whole point of
	// choosing a default rather than leaving one.
	//
	// The other direction is the dangerous one: a failure mode nobody has
	// classified yet is exactly the kind this deployment has just started
	// producing, and defaulting it to "not our problem" would let a new fault
	// arrive as a HEALTHY verdict. Defaulting it to ours costs a look at
	// something that turns out to be external; the reverse costs the signal.
	return AttributionOurs
}

// Attribute fills in the attribution on every anomaly in the list.
//
// It runs over the rows rather than being computed in the tracker so that the
// rule lives in one place and the page cannot disagree with the verdict: both
// read this field, neither re-derives it.
func Attribute(anomalies []Anomaly) {
	for index := range anomalies {
		anomalies[index].Attribution = attributionOf(anomalies[index])
	}
}

// OursCount returns how many of these count against the deployment.
func OursCount(anomalies []Anomaly) int {
	ours := 0
	for _, anomaly := range anomalies {
		if anomaly.Attribution == AttributionOurs {
			ours++
		}
	}
	return ours
}
