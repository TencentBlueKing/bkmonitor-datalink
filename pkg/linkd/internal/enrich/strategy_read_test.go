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

	"linkd/internal/enrich/models"
)

type strategyReadFunc func(context.Context, models.StrategyQuery) (models.CWStrategy, bool, error)

func (f strategyReadFunc) GetByStrategyID(ctx context.Context, query models.StrategyQuery) (models.CWStrategy, bool, error) {
	return f(ctx, query)
}

func TestStrategyReadPreservesEventIdentityAndCachesOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	scope, err := NewScope(testAlert(), Sources{CWStrategy: strategyReadFunc(func(_ context.Context, query models.StrategyQuery) (models.CWStrategy, bool, error) {
		calls.Add(1)
		if query != (models.StrategyQuery{TenantID: "tenant-1", ID: 123, Version: 1}) {
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
