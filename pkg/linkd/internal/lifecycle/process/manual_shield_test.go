// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
)

func TestManualShieldCapacityCancellationAndSlotRelease(t *testing.T) {
	command := lifecycle.ShieldCommand{TenantID: "tenant", AlertID: "alert", OperationID: "operation", OperatorID: "admin", ExpectedRevision: 1, Policy: domain.PolicyVersion{ID: "policy", Version: 1, Digest: strings.Repeat("a", 64)}, EffectiveAt: time.Now()}
	started := make(chan struct{}, 4)
	binder := &ManualShieldBinder{slots: make(chan struct{}, 4), run: func(ctx context.Context, _ lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return lifecycle.ShieldCommandResult{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 4)
	for range 4 {
		go func() { _, err := binder.BindShield(ctx, command); done <- err }()
	}
	for range 4 {
		<-started
	}
	if _, err := binder.BindShield(t.Context(), command); !errors.Is(err, policy.ErrPreviewCapacity) {
		t.Fatal("capacity not bounded", err)
	}
	cancel()
	for range 4 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	}
	if len(binder.slots) != 0 {
		t.Fatal("cancelled call retained slot")
	}
	if _, err := binder.BindShield(ctx, command); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled call reached backend", err)
	}
}
