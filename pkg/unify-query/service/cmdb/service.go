// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Service CMDB SchemaProvider 初始化服务
// 负责在 Redis 就绪后创建 CompositeSchemaProvider（Redis → Static 级联）
// 并注入到 v1beta1，使其能从 Redis 动态获取资源/关联配置
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc

	redisProvider *v1beta3.RedisSchemaProvider
	mu            sync.Mutex
}

func (s *Service) Type() string {
	return "cmdb"
}

func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

func (s *Service) Reload(ctx context.Context) {
	var err error
	ctx, span := trace.NewSpan(ctx, "cmdb-service-reload")
	defer span.End(&err)

	s.mu.Lock()
	defer s.mu.Unlock()

	// 关闭旧的 RedisSchemaProvider（如有）
	if s.redisProvider != nil {
		if closeErr := s.redisProvider.Close(); closeErr != nil {
			log.Warnf(ctx, "failed to close old RedisSchemaProvider: %v", closeErr)
		}
		s.redisProvider = nil
	}

	s.ctx, s.cancelFunc = context.WithCancel(ctx)

	// 检查 Redis 客户端是否就绪
	client := redis.Client()
	if client == nil {
		log.Warnf(ctx, "redis client not ready, v1beta1 will use hardcoded config as fallback")
		span.Set("schema_provider.status", "redis_not_ready")
		return
	}

	// 创建 RedisSchemaProvider
	redisProvider, redisErr := v1beta3.NewRedisSchemaProvider(client)
	if redisErr != nil {
		log.Warnf(ctx, "failed to create RedisSchemaProvider: %v, v1beta1 will use hardcoded config as fallback", redisErr)
		span.Set("schema_provider.status", "redis_provider_failed")
		span.Set("schema_provider.error", redisErr.Error())
		return
	}
	s.redisProvider = redisProvider

	// 创建 StaticSchemaProvider 作为兜底
	staticProvider := v1beta3.NewStaticSchemaProvider()

	// 创建 CompositeSchemaProvider: Redis(高优先级) → Static(兜底)
	compositeProvider := v1beta3.NewCompositeSchemaProvider(redisProvider, staticProvider)

	// 注入到 v1beta1 并触发 model 初始化
	v1beta1.InitSchemaProvider(compositeProvider)

	// 主动触发 v1beta1 model 构建，启动时即可确认配置加载结果
	if _, initErr := v1beta1.GetModel(ctx); initErr != nil {
		log.Errorf(ctx, "failed to initialize v1beta1 model: %v", initErr)
		span.Set("v1beta1.model.status", "failed")
		span.Set("v1beta1.model.error", initErr.Error())
	} else {
		log.Infof(ctx, "v1beta1 model initialized successfully")
		span.Set("v1beta1.model.status", "initialized")
	}

	log.Infof(ctx, "CMDB SchemaProvider initialized: CompositeSchemaProvider(Redis → Static) injected into v1beta1")
	span.Set("schema_provider.status", "initialized")
	span.Set("schema_provider.type", "composite(redis→static)")
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.redisProvider != nil {
		if err := s.redisProvider.Close(); err != nil {
			log.Warnf(context.TODO(), "failed to close RedisSchemaProvider: %v", err)
		}
		s.redisProvider = nil
	}

	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}

func (s *Service) Wait() {
	// RedisSchemaProvider 的 goroutine 由其自身的 wg 管理，Close() 时会等待
}
