// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/store"
)

// 隐藏可选 fast 接口，复现不经遥测包装的 MySQL Repository 能力边界。
type deliveryReadFixture struct {
	store.Repository
	store.ProjectionWorkStore
	store.ActionWorkStore
	read func(context.Context, string, string) (store.StoredAlert, error)
}

func (f *deliveryReadFixture) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return f.read(ctx, tenant, id)
}

type deliveryFastReadFixture struct {
	*deliveryReadFixture
	current func(context.Context, string, string) (store.StoredAlert, error)
}

func (f *deliveryFastReadFixture) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return f.current(ctx, tenant, id)
}

func TestDeliveryBusinessCurrentReadDoesNotDependOnTelemetry(t *testing.T) {
	for _, fast := range []bool{false, true} {
		failure := errors.New("read failed")
		ordinaryCalls, currentCalls := 0, 0
		base := &deliveryReadFixture{read: func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
			if ctx != t.Context() || tenant != "tenant" || id != "alert" {
				t.Error("read lost scope/context")
			}
			ordinaryCalls++
			return store.StoredAlert{}, failure
		}}
		var repository store.Repository = base
		if fast {
			repository = &deliveryFastReadFixture{deliveryReadFixture: base, current: func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				if ctx != t.Context() || tenant != "tenant" || id != "alert" {
					t.Error("current read lost scope/context")
				}
				currentCalls++
				return store.StoredAlert{}, failure
			}}
		}
		adapter, err := newDeliveryBusinessStore(repository)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.GetAlertCurrent(t.Context(), "tenant", "alert"); !errors.Is(err, failure) {
			t.Fatal("read failure swallowed", err)
		}
		if fast && (ordinaryCalls != 0 || currentCalls != 1) || !fast && (ordinaryCalls != 1 || currentCalls != 0) {
			t.Fatal("wrong read consistency path", fast, ordinaryCalls, currentCalls)
		}
	}
	if _, err := newDeliveryBusinessStore(struct{ store.Repository }{}); err == nil {
		t.Fatal("missing work readers accepted")
	}
}
