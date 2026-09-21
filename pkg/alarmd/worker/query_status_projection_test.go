// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every physical query that carried a status is reported, because this feeds a
// counter rather than a log line. A Query Group holds one physical query per
// Plan, so reporting only the first would undercount by however many Plans
// share the group - and a Query Group is shared across strategies by design.
func TestEveryPhysicalQueryStatusIsProjected(t *testing.T) {
	withStatus := func(code string, allowed bool) execution.PhysicalQueryCompletion {
		return execution.PhysicalQueryCompletion{
			RouteFacts: execution.ProviderRouteFacts{Status: &execution.ProviderStatusFact{Code: code, Allowed: allowed}},
		}
	}
	completion := execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		withStatus("SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS", true),
		// A healthy physical query in the same completion. Mixed is the normal
		// case, and dropping the ones that carried a code because a neighbour
		// did not is exactly the loss this counter exists to prevent.
		{},
		withStatus("STORAGE_TIMEOUT", false),
	}}

	facts := providerStatusFacts(completion)
	if len(facts) != 2 {
		t.Fatalf("facts=%+v, want both status-carrying queries", facts)
	}
	if facts[0].Code != "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS" || facts[0].Outcome != observability.QueryStatusOutcomeAllowed {
		t.Fatalf("facts[0]=%+v, want the allowed one", facts[0])
	}
	if facts[1].Code != "STORAGE_TIMEOUT" || facts[1].Outcome != observability.QueryStatusOutcomeUnavailable {
		t.Fatalf("facts[1]=%+v, want the unavailable one", facts[1])
	}
}

// A completion where nothing carried a status projects nothing, so a healthy
// Slot does not land in a bucket.
func TestCompletionWithoutStatusProjectsNothing(t *testing.T) {
	completion := execution.QueryExecutionCompletion{
		PhysicalQueries: []execution.PhysicalQueryCompletion{{}, {}},
	}
	if facts := providerStatusFacts(completion); len(facts) != 0 {
		t.Fatalf("facts=%+v, want none", facts)
	}
}
