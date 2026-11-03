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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// failingDependencyProvider answers the primary window and fails the query
// whose window does not end at the Slot: the algorithm dependency.
type failingDependencyProvider struct {
	windowedHostProvider
	slotEnd int64
}

func (provider *failingDependencyProvider) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	if attempt.Spec.LogicalWindow.End != provider.slotEnd {
		return execution.ProviderCompletion{}, errors.New("dependency read failed")
	}
	return provider.windowedHostProvider.Execute(ctx, attempt, sink)
}

// A Slot's primary and its dependency are planned under different deadlines.
// When the dependency fails, its failure carries its own role and planned
// deadlines, not the primary's nor the round's binding consumer's: the line
// says which input failed and the time it was given.
func TestAFailedDependencySaysItsOwnRoleAndPlannedDeadline(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	withYesterdayDependency(t, &frozen)
	dependency := &frozen.Requirements[len(frozen.Requirements)-1]
	dependency.Consumers = append([]execution.DataRequirementConsumer(nil), dependency.Consumers...)
	dependency.Consumers[0].DownstreamExecutionReserveMilliSec += 7_000
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	slotEnd := int64(contractRef.Slot.EvaluationTime)

	primary := frozen.Requirements[0].Consumers[0]
	primaryFrozen := primary.ConsumerDeadlineUnixMilli - primary.DownstreamExecutionReserveMilliSec
	dependencyFrozen := dependency.Consumers[0].ConsumerDeadlineUnixMilli - dependency.Consumers[0].DownstreamExecutionReserveMilliSec
	if primaryFrozen == dependencyFrozen {
		t.Fatalf("the test needs two planned deadlines, both are %d", primaryFrozen)
	}

	source, err := NewSource(staticFrozenPlan{plan: frozen},
		&failingDependencyProvider{windowedHostProvider: windowedHostProvider{hosts: []string{"192.0.2.10"}}, slotEnd: slotEnd},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.Unix(slotEnd, 0) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	ctx, capture := observability.ContextWithBudgetCapture(context.Background())
	_, err = source.Execute(ctx, execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, &recordingConsumer{})
	if err == nil {
		t.Fatal("the dependency failed, the round did not")
	}

	var planned interface {
		QueryFailurePlanned() *execution.PlannedQueryFacts
	}
	if !errors.As(err, &planned) || planned.QueryFailurePlanned() == nil {
		t.Fatalf("the failure carries no plan: %v", err)
	}
	facts := planned.QueryFailurePlanned()
	if facts.Role != string(execution.InputRoleAlgorithmDependency) || facts.Dataset != "yesterday" {
		t.Fatalf("role %q dataset %q, want the dependency's", facts.Role, facts.Dataset)
	}
	if facts.FrozenDeadlineUnixMilli != dependencyFrozen || facts.DeadlineUnixMilli != dependencyFrozen {
		t.Fatalf("planned %d frozen %d, want the dependency's own %d (the primary's is %d)",
			facts.DeadlineUnixMilli, facts.FrozenDeadlineUnixMilli, dependencyFrozen, primaryFrozen)
	}
	if facts.CompletionDeadlineUnixMilli != dependency.Consumers[0].ConsumerDeadlineUnixMilli || facts.FellBack {
		t.Fatalf("completion %d fell back %v", facts.CompletionDeadlineUnixMilli, facts.FellBack)
	}

	// The round's line takes the failed query's plan, beside the binding
	// consumer's budget.
	budget := capture.Facts()
	if budget == nil || budget.FailedPlanned == nil {
		t.Fatalf("the round's budget keeps no failed plan: %+v", budget)
	}
	if budget.FailedPlanned.Role != facts.Role || budget.FailedPlanned.DeadlineUnixMilli != dependencyFrozen {
		t.Fatalf("the round's failed plan is %+v, want the dependency's", budget.FailedPlanned)
	}
}
