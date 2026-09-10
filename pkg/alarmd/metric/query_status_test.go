// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func statusObservation(code, outcome string) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Operation: observability.OperationNormal, Result: observability.ResultSuccess,
		QueryStatus: []observability.QueryStatusFacts{{Code: code, Outcome: outcome}},
	}
}

// Allowing a status code through and letting it decide the completion are
// opposite outcomes for the same code, and only counting them apart says which
// one a deployment is doing. Without this, allowing the code makes it act on
// nothing and be counted nowhere: a table that really disappeared then reports
// a steady healthy value from the expression's own fallback, which is quieter
// than the outage that prompted allowing it in the first place.
func TestQueryStatusCountsWhatTheDeploymentDidNotJustWhatUQSaid(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(),
		statusObservation(observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed))
	recorder.Observe(context.Background(),
		statusObservation(observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed))
	recorder.Observe(context.Background(),
		statusObservation(observability.QueryStatusStorageTimeout, observability.QueryStatusOutcomeUnavailable))

	allowed := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed))
	if allowed != 2 {
		t.Fatalf("allowed = %v, want the two responses that were kept", allowed)
	}
	refused := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusStorageTimeout, observability.QueryStatusOutcomeUnavailable))
	if refused != 1 {
		t.Fatalf("unavailable = %v, want the one response that was discarded", refused)
	}
	// The same code under the other outcome must stay at zero, or the metric
	// cannot be used to tell "we are running on fallbacks" from "we are
	// refusing responses".
	crossed := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeUnavailable))
	if crossed != 0 {
		t.Fatalf("the two outcomes were not counted apart: %v", crossed)
	}
}

// UQ can add a code without telling us. Counting it as OTHER keeps the label
// set closed here rather than letting another component move this metric's
// cardinality, and still shows that something unrecognised arrived -- which a
// dropped observation would not.
func TestQueryStatusCountsAnUnrecognisedCodeAsOtherRatherThanDroppingIt(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(),
		statusObservation("SOME_CODE_THIS_BUILD_HAS_NEVER_SEEN", observability.QueryStatusOutcomeUnavailable))

	other := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusOther, observability.QueryStatusOutcomeUnavailable))
	if other != 1 {
		t.Fatalf("OTHER = %v, want the unrecognised code counted there", other)
	}
	invented := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		"SOME_CODE_THIS_BUILD_HAS_NEVER_SEEN", observability.QueryStatusOutcomeUnavailable))
	if invented != 0 {
		t.Fatalf("an unrecognised code became its own label value: %v", invented)
	}
}

// Most responses carry no status at all. Counting those would bury the codes
// that matter under the volume of ordinary traffic, so they are not counted --
// and this pins that a response without a code is silent here rather than
// arriving as OTHER.
func TestQueryStatusIgnoresResponsesThatCarriedNoCode(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), statusObservation("", observability.QueryStatusOutcomeAllowed))
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Result: observability.ResultSuccess,
	})

	for _, outcome := range []string{observability.QueryStatusOutcomeAllowed, observability.QueryStatusOutcomeOther} {
		if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
			observability.QueryStatusOther, outcome)); got != 0 {
			t.Fatalf("a response with no status code was counted as OTHER/%s: %v", outcome, got)
		}
	}
}

// The facts belong to one place in the pipeline. Accepting them from anywhere
// would let an unrelated stage inflate the count that is supposed to mean
// "a UQ response arrived carrying this code".
func TestQueryStatusOnlyCountsTheAccessQueryCompletion(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observation := statusObservation(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed)
	observation.Stage = observability.StageEvaluationCompleted
	recorder.Observe(context.Background(), observation)

	if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed)); got != 0 {
		t.Fatalf("a stage that does not read UQ responses was counted: %v", got)
	}
}

// A query group with several Plans completes one query_completed carrying
// several physical queries, each able to report its own status. Counting the
// first would report a number that is simply wrong -- tolerable for a log
// field, which loses a line, not for a counter, which is then read as a rate.
func TestQueryStatusCountsEveryPhysicalQueryNotJustTheFirst(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Result: observability.ResultSuccess,
		QueryStatus: []observability.QueryStatusFacts{
			{Code: observability.QueryStatusSpaceTableIDNotExists, Outcome: observability.QueryStatusOutcomeAllowed},
			{Code: observability.QueryStatusSpaceTableIDNotExists, Outcome: observability.QueryStatusOutcomeAllowed},
			{Code: observability.QueryStatusStorageTimeout, Outcome: observability.QueryStatusOutcomeUnavailable},
		},
	})

	if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed)); got != 2 {
		t.Fatalf("allowed = %v, want both physical queries counted", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusStorageTimeout, observability.QueryStatusOutcomeUnavailable)); got != 1 {
		t.Fatalf("unavailable = %v, want the third physical query counted", got)
	}
}

// The entries that carried no code are skipped without dropping the ones that
// did. A completion mixing "answered normally" with "answered with a code" is
// the ordinary case, and losing the coded one there would hide exactly what
// this counts.
func TestQueryStatusKeepsCodedEntriesBesideUncodedOnes(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Result: observability.ResultSuccess,
		QueryStatus: []observability.QueryStatusFacts{
			{Code: "", Outcome: observability.QueryStatusOutcomeAllowed},
			{Code: observability.QueryStatusSpaceTableIDNotExists, Outcome: observability.QueryStatusOutcomeAllowed},
		},
	})

	if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusSpaceTableIDNotExists, observability.QueryStatusOutcomeAllowed)); got != 1 {
		t.Fatalf("the coded entry was lost beside an uncoded one: %v", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.queryStatus.responses.WithLabelValues(
		observability.QueryStatusOther, observability.QueryStatusOutcomeAllowed)); got != 0 {
		t.Fatalf("an entry with no code was counted as OTHER: %v", got)
	}
}
