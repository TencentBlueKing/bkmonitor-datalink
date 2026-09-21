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
	"testing"
)

func TestPreparationSharesRetainedAccountWithoutWaiting(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 100}}
	release, err := co.reservePreparationBytes(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := co.acquireProvisional(1, 30, nil, "normal_input"); err != nil {
		t.Fatal(err)
	}
	_, err = co.reservePreparationBytes(context.Background(), 11)
	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) || co.reservations.retainedBytes != 90 {
		t.Fatalf("failed incremental reservation mutated account: bytes=%d err=%v", co.reservations.retainedBytes, err)
	}
	release()
	release()
	if co.reservations.retainedBytes != 30 {
		t.Fatal(co.reservations.retainedBytes)
	}
	co.releaseProvisional(1, 30)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := co.reservePreparationBytes(ctx, 50); !errors.Is(err, context.Canceled) || co.reservations.retainedBytes != 0 {
		t.Fatalf("cancelled preparation retained bytes: %d %v", co.reservations.retainedBytes, err)
	}
}
