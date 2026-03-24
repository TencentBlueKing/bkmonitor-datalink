// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func buildAllInfosCache(ctx context.Context, bkTenantId, prefix string, redisOpt *redis.Options, concurrentLimit int, cacheTypes ...string) {
	var wg sync.WaitGroup
	n := time.Now()

	for _, cacheType := range cacheTypes {
		wg.Add(1)
		go func(cacheType string) {
			defer wg.Done()
			cacheManager, err := NewCacheManagerByType(bkTenantId, redisOpt, prefix, cacheType, concurrentLimit)
			if err != nil {
				logger.Warnf("[cmdb_relation] failed to create cache manager for type: %s, error: %v", cacheType, err)
				return
			}
			err = cacheManager.BuildRelationMetrics(ctx)
			if err != nil {
				logger.Warnf("[cmdb_relation] failed to build relation metrics for type: %s, error: %v", cacheType, err)
			}
		}(cacheType)
	}
	wg.Wait()

	logger.Infof("[cmdb_relation] build_all_cache action:end cost: %s", time.Since(n))
}
