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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The query budget observation names the strategy with both halves of its
// identity, the way every other observation of the round does. It named the
// strategy alone on a live deployment, and the fleet -- which learns an
// object's strategies from whatever traces reach it -- listed each strategy
// twice, once with its business and once without.
func TestTheQueryBudgetObservationNamesTheStrategyWithItsBusiness(t *testing.T) {
	ref, frozen := frozenExecution(t)
	frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 15, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
	eval := int64(ref.Slot.EvaluationTime) * 1000
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = eval + 30000
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = eval + 30000
	ref = bindFrozenDueDigest(t, ref, frozen)
	var facts []observability.Observation
	source, _ := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{},
		Config{MinReadyDelay: 30 * time.Second, Now: func() time.Time { return time.UnixMilli(eval) },
			Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { facts = append(facts, o) })})
	_, _ = source.Execute(context.Background(), execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationNormal, AttemptNo: 1}, &recordingConsumer{})
	plan := frozen.Requirements[0].Consumers[0].Consumer.Plan
	found := false
	for _, fact := range facts {
		if fact.Stage != observability.StageQueryBudgetResolved {
			continue
		}
		found = true
		if fact.Trace.StrategyID != plan.StrategyID || fact.Trace.BusinessID != plan.BusinessID || plan.BusinessID == "" {
			t.Fatalf("query budget trace names strategy %q business %q; the consumer's Plan is %+v", fact.Trace.StrategyID, fact.Trace.BusinessID, plan)
		}
	}
	if !found {
		t.Fatalf("no %s observation among %d facts", observability.StageQueryBudgetResolved, len(facts))
	}
}
