// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

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
	// AttributionUnknown is an object with no evidence recorded either way.
	//
	// It exists because the first live read of this split had nineteen objects
	// against the deployment and seven of them were only there for want of a
	// record: restored from persisted state, which keeps the completion kind and
	// not the cause, four minutes after a rollout. Counting those as ours makes
	// the verdict DEGRADED after every deploy for as long as it takes each object
	// to finish one more round -- which is the failure this split was built to
	// remove, arriving by a different route.
	//
	// It is not the same as an unrecognised code. A code nobody has classified
	// is a failure mode this build is producing and counts against it; no code at
	// all, on an object this process never watched fail, is missing evidence. The
	// first is a verdict, the second is the absence of one.
	AttributionUnknown Attribution = "UNKNOWN"
)

// attributionOf decides which side one anomaly falls on, from its finding.
//
// Only ALARMD's checks decide the verdict. What nobody can yet hand to anyone
// stays on the reader's list but does not degrade the deployment -- with one
// exception, the object restored without a cause, which is missing evidence
// and keeps the verdict UNKNOWN: not evidence against the deployment, and not
// evidence of health either.
func attributionOf(anomaly Anomaly) Attribution {
	if restoredWithoutEvidence(anomaly) {
		return AttributionUnknown
	}
	owner := anomaly.Finding.Owner
	if owner == "" {
		// Not attributed yet: decide the line now, on whatever schedule the
		// finding carries. attribute() always sets the owner first, so this is
		// the path for a caller asking about one anomaly on its own.
		owner = OwnerNobody
		if check, under, _ := checkOf(anomaly, anomaly.Finding.Schedule); under {
			owner = checkAnswers[check].Owner
		}
	}
	if owner == OwnerAlarmd {
		return AttributionOurs
	}
	return AttributionExternal
}

// restoredWithoutEvidence reports an object rebuilt from a record that does not
// carry why it was failing.
//
// The completion kind is persisted and the cause below it is not, so a restored
// object arrives with a kind and nothing else. That is missing evidence about a
// real anomaly, not evidence of a healthy one and not evidence against the
// deployment.
func restoredWithoutEvidence(anomaly Anomaly) bool {
	if anomaly.Cause != "" || anomaly.CauseReason != "" {
		return false
	}
	if anomaly.Failure != nil && anomaly.Failure.Code != "" {
		return false
	}
	for _, restored := range RestoredSinceSources {
		if anomaly.SinceFrom == restored {
			return true
		}
	}
	return false
}

// UnattributedCount returns how many carry no evidence either way.
func UnattributedCount(anomalies []Anomaly) int {
	unknown := 0
	for _, anomaly := range anomalies {
		if anomaly.Attribution == AttributionUnknown {
			unknown++
		}
	}
	return unknown
}

// Attribute fills in the attribution on every anomaly in the list.
//
// It runs over the rows rather than being computed in the tracker so that the
// rule lives in one place and the page cannot disagree with the verdict: both
// read this field, neither re-derives it.
func Attribute(anomalies []Anomaly, at time.Time) {
	for index := range anomalies {
		attribute(&anomalies[index], at)
	}
}

// attribute decides everything the page reads about one object, in one place.
//
// The two dimensions first, then the check on the evidence and the dimensions,
// then the owner from the check and the attribution from the owner. One
// decision, and the verdict and the page read the same fields of it.
//
// It is called again by MarkStalled for the objects it marks, because Stalled
// is decided after the view is built and changes the answer to every question
// here: a stalled object is this deployment's whatever its last code said.
// Before that, only the attribution was rewritten, so a stalled row rendered
// as the backend's or the strategy's.
func attribute(anomaly *Anomaly, at time.Time) {
	schedule := scheduleOf(*anomaly, at)
	// The failure in the one shape every failure is read in, before the
	// check: the fold on it is one of the group keys below.
	anomaly.Blocked = blockedOf(*anomaly, schedule)
	check, under, unclassified := checkOf(*anomaly, schedule)
	finding := Finding{Owner: OwnerNobody, Schedule: schedule, Result: resultOf(*anomaly)}
	if under {
		finding.Check, finding.Group, finding.Owner = check, groupKeyOf(*anomaly, check), checkAnswers[check].Owner
	}
	anomaly.Finding = finding
	// Recorded per object rather than derived twice, so the page and the
	// counts cannot disagree about which of these was actually decided.
	anomaly.Unclassified = unclassified
	anomaly.Attribution = attributionOf(*anomaly)
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
