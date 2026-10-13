// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func failedAttempt(reason execution.ReasonCode, detail string) execution.ProviderCompletion {
	return execution.ProviderCompletion{Completeness: execution.CompletenessUnavailable, RouteFacts: execution.ProviderRouteFacts{
		Attempts: []execution.RouteAttemptFact{{Result: execution.RouteAttemptFailed, ReasonCode: reason, Detail: detail}}}}
}

// The downstream is in distress when it says it is overloaded or cannot
// answer: a timeout, a 429, a 502/503/504, or a connection refused, reset or
// cut. A query it refused as malformed, or answered, is not.
func TestDownstreamDistressIsOverloadAndUnreachabilityOnly(t *testing.T) {
	unavailable := execution.ReasonCode(contract.ReasonQueryUnavailable)
	for _, test := range []struct {
		completion execution.ProviderCompletion
		want       string
	}{
		{failedAttempt(execution.ReasonCode(contract.ReasonQueryTimeout), execution.TransportRouteDetail(execution.TransportFailureTimeout)), DistressTimeout},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(429)), DistressThrottled},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(502)), DistressUnavailable},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(503)), DistressUnavailable},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(504)), DistressUnavailable},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureConnectionRefused)), DistressUnreachable},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureConnectionReset)), DistressUnreachable},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureEOF)), DistressUnreachable},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureTimeout)), DistressTimeout},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(400)), ""},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(500)), ""},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureDNS)), ""},
		{execution.ProviderCompletion{Completeness: execution.CompletenessFull}, ""},
	} {
		if got := downstreamDistressOf(test.completion); got != test.want {
			t.Fatalf("%+v: distress %q, want %q", test.completion.RouteFacts.Attempts, got, test.want)
		}
	}
}

type distressProvider struct{ completion execution.ProviderCompletion }

func (provider distressProvider) Execute(context.Context, execution.QueryAttempt, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	return provider.completion, nil
}

type waitedPermit struct {
	waited   bool
	released *int
}

func (permit waitedPermit) RecoveryPermit() *execution.RecoveryPermit { return nil }
func (permit waitedPermit) Release()                                  { *permit.released++ }
func (permit waitedPermit) Waited() bool                              { return permit.waited }

type distressCall struct {
	class     string
	contended bool
}

// Each distressed answer is counted once, with whether the query had waited
// for its permit: the reading that says whether the downstream's distress
// lines up with this process's own contention.
func TestADistressedAnswerIsCountedWithWhetherItsQueryWaited(t *testing.T) {
	var calls []distressCall
	released := 0
	for _, waited := range []bool{true, false} {
		for _, completion := range []execution.ProviderCompletion{
			failedAttempt(execution.ReasonCode(contract.ReasonQueryUnavailable), execution.HTTPStatusRouteDetail(503)),
			{Completeness: execution.CompletenessFull},
		} {
			source := &Source{provider: distressProvider{completion: completion}, config: Config{
				ObserveDownstreamDistress: func(class string, contended bool) { calls = append(calls, distressCall{class, contended}) }}}
			if _, err := source.executeWithPermit(context.Background(), execution.QueryAttempt{}, nil, waitedPermit{waited: waited, released: &released}); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []distressCall{{DistressUnavailable, true}, {DistressUnavailable, false}}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("counted %+v, want %+v", calls, want)
	}
	if released != 4 {
		t.Fatalf("released %d permits of 4", released)
	}
}
