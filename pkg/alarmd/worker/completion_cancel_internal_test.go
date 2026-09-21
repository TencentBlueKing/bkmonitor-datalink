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
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

type completionCancelPorts struct {
	*planFailurePorts
	cancel         context.CancelFunc
	block          bool
	ack            bool
	stateCancelled bool
}

func (p *completionCancelPorts) WriteBatch(ctx context.Context, _ []contract.TriggerEventV1) error {
	if p.block {
		<-ctx.Done()
		return ctx.Err()
	}
	p.ack = true
	p.cancel()
	return nil
}
func (p *completionCancelPorts) ApplyRuntime(ctx context.Context, r execution.StateApplyRequest) (execution.StateApplyResult, error) {
	if err := ctx.Err(); err != nil {
		p.stateCancelled = true
		return execution.StateApplyResult{}, err
	}
	return p.planFailurePorts.ApplyRuntime(ctx, r)
}

func TestShortCompletionDownstreamCancellationPreservesACKStateProgressOrder(t *testing.T) {
	for _, block := range []bool{true, false} {
		f := newPlanIsolationFixture(t, nil)
		spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 10, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
		header := execution.InternalExecutionHeader{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: execution.EvaluationTime(time.Now().Unix() - 29)}}}
		deadline, _ := spec.CompletionDeadlineUnixMilli(header.Contract.Slot.EvaluationTime)
		header.DuePlans = []execution.DuePlan{{ScheduleSpec: spec, CompletionDeadlineUnixMilli: deadline}}
		ctx, cancel := shortPeriodCompletionContext(context.Background(), execution.OperationNormal, header)
		ports := &completionCancelPorts{planFailurePorts: f.ports, cancel: cancel, block: block}
		f.coordinator.ports.Events = ports
		f.coordinator.ports.State = ports
		result, err := f.coordinator.finalizePrepared(ctx, f.request, f.header, f.bindings, f.loaded, f.evaluated)
		cancel()
		if (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) || result.Completed || result.CompletionKind != "" {
			t.Fatalf("block=%v result=%+v error=%v", block, result, err)
		}
		if len(f.base.stateApplied) != 0 || f.base.progressCommits != 0 {
			t.Fatal("cancelled State/Progress advanced")
		}
		if !block && (!ports.ack || !ports.stateCancelled) {
			t.Fatal("ACK success did not propagate cancellation to State")
		}
	}
}

func (p *completionCancelPorts) RenewFrozenRuntime(
	_ context.Context, request execution.FrozenStateRenewalRequest,
) (execution.FrozenStateRenewalResult, error) {
	return freshFrozenRenewals(request), nil
}
