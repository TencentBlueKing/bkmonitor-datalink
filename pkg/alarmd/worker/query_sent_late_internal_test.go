// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func timedOutAttempt(detail string, elapsed, startLate int64) execution.ProviderRouteFacts {
	return execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{
		Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout), Detail: detail,
		Timing: &execution.AttemptTiming{ElapsedMillis: elapsed, StartLateMillis: startLate},
	}}}
}

// A timeout is either the backend's - it had the query longer than alarmd
// spent before sending it - or a query sent late, which proves nothing about
// the backend. The two are told apart the same way, so one never reads as
// both; alarmd's own delivery running out the deadline is neither, and
// neither is any other failure.
func TestATimeoutIsTheBackendsOrALateQuerysNeverBoth(t *testing.T) {
	transport := execution.TransportRouteDetail(execution.TransportFailureTimeout)
	for _, test := range []struct {
		name              string
		routes            execution.ProviderRouteFacts
		backend, sentLate bool
	}{
		{"sent on time, timed out", timedOutAttempt(transport, 30_000, 500), true, false},
		{"sent 23 s late, timed out after 13", timedOutAttempt(transport, 13_000, 23_000), false, true},
		{"the answer stopped partway, sent late", timedOutAttempt(execution.BodyRouteDetail(execution.TransportFailureTimeout), 13_000, 23_000), false, true},
		{"alarmd's own delivery ran out", timedOutAttempt(execution.DeliveryTimeoutRouteDetail, 13_000, 23_000), false, false},
		{"an unavailable answer", execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{Result: execution.RouteAttemptFailed,
			ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable), Timing: &execution.AttemptTiming{ElapsedMillis: 1, StartLateMillis: 9}}}}, false, false},
	} {
		if backend, sentLate := backendDidNotAnswer(test.routes), timedOutSentLate(test.routes); backend != test.backend || sentLate != test.sentLate {
			t.Errorf("%s: backend %t, sent late %t; want %t, %t", test.name, backend, sentLate, test.backend, test.sentLate)
		}
	}
}

// Only a primary query sent late and timed out names QUERY_TIMEOUT as why
// its Slot proved nothing; a dependency's does not, and a Slot that proved
// something has no unknown reason.
func TestOnlyALatePrimaryTimeoutIsTheUnknownReason(t *testing.T) {
	unavailable := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessUnavailable}
	primary := execution.NamedInputBinding{Role: execution.InputRolePrimary, ReasonCode: contract.ReasonQueryTimeout}
	var late queryAvailabilityEvidence
	late.observe(primary, unavailable, false, false)
	late.observeSentLate(primary, true)
	if late.availability() != execution.QueryAvailabilityUnknown || late.unknownReason() != execution.ReasonCode(contract.ReasonQueryTimeout) {
		t.Fatalf("a late primary timeout: %v, %q; want unknown for a timeout", late.availability(), late.unknownReason())
	}
	var dependency queryAvailabilityEvidence
	dependency.observe(primary, unavailable, false, false)
	dependency.observeSentLate(execution.NamedInputBinding{Role: execution.InputRoleAlgorithmDependency}, true)
	if dependency.unknownReason() != "" {
		t.Fatalf("a late dependency timeout named %q, want nothing", dependency.unknownReason())
	}
	var backend queryAvailabilityEvidence
	backend.observe(primary, unavailable, false, true)
	backend.observeSentLate(primary, true)
	if backend.availability() != execution.QueryAvailabilityUnavailable || backend.unknownReason() != "" {
		t.Fatalf("the backend's timeout: %v, %q; want unavailable and no unknown reason", backend.availability(), backend.unknownReason())
	}
}
