// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
)

type shortScheduleConsumer struct {
	execution.QueryExecutionConsumer
}

func (c shortScheduleConsumer) Begin(ctx context.Context, h execution.InternalExecutionHeader) error {
	setShortSchedule(&h)
	return c.QueryExecutionConsumer.Begin(ctx, h)
}

func setShortSchedule(h *execution.InternalExecutionHeader) {
	for i := range h.DuePlans {
		h.DuePlans[i].ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 10, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
		h.DuePlans[i].CompletionDeadlineUnixMilli, _ = h.DuePlans[i].ScheduleSpec.CompletionDeadlineUnixMilli(h.Contract.Slot.EvaluationTime)
		for r := range h.Requirements {
			for j := range h.Requirements[r].Consumers {
				if h.Requirements[r].Consumers[j].Consumer.Plan == h.DuePlans[i].Identity {
					h.Requirements[r].Consumers[j].ConsumerDeadlineUnixMilli = h.DuePlans[i].CompletionDeadlineUnixMilli
				}
			}
		}
	}
}

func TestShortPeriodExpiredAfterQueryNeverCompletesAndCoordinatorRemainsUsable(t *testing.T) {
	f := newFixture(t, true, "")
	f.ports.executeOverride = func(ctx context.Context, r execution.QueryExecutionRequest, c execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
		f.ports.executeOverride = nil
		return f.ports.Execute(ctx, r, shortScheduleConsumer{c})
	}
	request := slotRequest(execution.OperationNormal)
	input := validInternalExecution()
	header := execution.InternalExecutionHeader{Contract: request.Contract, DuePlans: input.DuePlans, Requirements: input.Requirements}
	setShortSchedule(&header)
	digest, err := execution.DeriveDuePlanSetDigest(header.DuePlans, header.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	request.Contract.DuePlanSetDigest = digest
	request.DuePlanTargets.DuePlanSetDigest = digest
	result, err := f.coordinator.Execute(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) || result.Completed || result.CompletionKind != "" {
		t.Fatalf("expired result=%+v error=%v", result, err)
	}
	for _, step := range *f.trace {
		if step == "evaluate" || step == "event_write" || step == "progress_commit" {
			t.Fatalf("expired execution reached %s", step)
		}
	}
	result, err = f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || result.CompletionKind == "" {
		t.Fatalf("healthy execution result=%+v error=%v", result, err)
	}
}
