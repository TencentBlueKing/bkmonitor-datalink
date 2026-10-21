// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

type strategyReadFunc func(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error)

func (f strategyReadFunc) GetByStrategyID(ctx context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	return f(ctx, query)
}

func TestStrategyReadPreservesEventIdentityAndCachesOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	scope, err := NewScope(testAlert(), Sources{CWStrategy: strategyReadFunc(func(_ context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
		calls.Add(1)
		if query != (models.StrategyRevisionQuery{TenantID: "tenant-1", ID: 123, Version: 1}) {
			return models.CWStrategy{}, false, fmt.Errorf("unexpected strategy identity: %+v", query)
		}
		return models.CWStrategy{Status: models.CWStrategyStatus{BKStrategyID: query.ID}}, true, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			value, found, err := scope.CWStrategy(t.Context())
			if err != nil || !found || value.Status.BKStrategyID != 123 {
				t.Errorf("strategy found=%t err=%v", found, err)
			}
		})
	}
	readers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("strategy read %d times", calls.Load())
	}
}

// TestStrategyReadDoesNotRequireBusiness 保留租户、ID、版本查询，不读取事件的业务标签。
func TestStrategyReadDoesNotRequireBusiness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   domain.Scalar
		missing bool
	}{
		{name: "missing", missing: true},
		{name: "non-BKCC space", value: domain.NewStringScalar("-255")},
		{name: "invalid business type", value: domain.NewStringScalar("not-a-business")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := testAlert()
			if tc.missing {
				delete(event.Labels, labelBizID)
			} else {
				event.Labels[labelBizID] = tc.value
			}
			calls := 0
			scope, err := NewScope(event, Sources{CWStrategy: strategyReadFunc(func(_ context.Context, q models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
				calls++
				if q != (models.StrategyRevisionQuery{TenantID: event.BKTenantID, ID: 123, Version: 1}) {
					t.Fatalf("identity=%+v", q)
				}
				return models.CWStrategy{}, true, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, found, err := scope.CWStrategy(t.Context()); err != nil || !found {
					t.Fatalf("found=%t error=%v", found, err)
				}
			}
			if calls != 1 {
				t.Fatalf("reader calls=%d", calls)
			}
		})
	}
}

func TestStrategyReadRejectsInvalidIdentityWithoutQuery(t *testing.T) {
	t.Parallel()
	for _, field := range []string{labelStrategyID, labelStrategyVersion} {
		t.Run(field, func(t *testing.T) {
			event := testAlert()
			delete(event.Labels, field)
			scope, err := NewScope(event, Sources{CWStrategy: strategyReadFunc(func(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
				t.Fatal("invalid identity reached reader")
				return models.CWStrategy{}, false, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := scope.CWStrategy(t.Context()); found || err == nil {
				t.Fatalf("found=%t error=%v", found, err)
			}
		})
	}
}
