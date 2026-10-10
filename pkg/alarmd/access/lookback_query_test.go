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
)

// The lookback hears when a query was ready and the Slot its schedule has
// next, which the Runner put on the context, and without one, none.
func TestTheLookbackHearsWhenTheQueryWasReadyAndTheSlotAfter(t *testing.T) {
	request := execution.QueryExecutionRequest{Contract: execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 120}}, Operation: execution.OperationNormal, AttemptNo: 1}
	ctx := execution.WithFollowingSlot(context.Background(), 180)
	q := lookbackQuery(ctx, request, execution.PhysicalQuerySpec{Digest: "d"}, 1, 150_000)
	if q.FollowingSlot != 180 || !q.ReadyAt.Equal(time.UnixMilli(150_000)) || q.Contract != request.Contract || q.Spec.Digest != "d" ||
		q.AttemptNo != 1 || q.Operation != execution.OperationNormal {
		t.Fatalf("lookback query %+v, want the Slot after and the ready moment", q)
	}
	if q := lookbackQuery(context.Background(), request, execution.PhysicalQuerySpec{}, 1, 150_000); q.FollowingSlot != 0 {
		t.Fatalf("following Slot %d with none on the context, want none", q.FollowingSlot)
	}
}
