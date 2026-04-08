// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

type Service struct {
	ctx             context.Context
	cancelFunc      context.CancelFunc
	providerManager *relation.ProviderManager
}

func (s *Service) Type() string {
	return "redis"
}

func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

func (s *Service) Reload(ctx context.Context) {
	// 关闭上一次的redis instance
	s.Close()

	log.Debugf(context.TODO(), "waiting for redis service close")
	// 等待上一个注册彻底关闭
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "redis service context update success.")

	options := &goRedis.UniversalOptions{
		MasterName:       MasterName,
		DB:               DataBase,
		Password:         Password,
		SentinelPassword: SentinelPassword,
		DialTimeout:      DialTimeout,
		ReadTimeout:      ReadTimeout,
	}

	// 兼容哨兵模式
	if Mode == "sentinel" {
		options.Addrs = SentinelAddress
	} else {
		options.Addrs = []string{fmt.Sprintf("%s:%d", Host, Port)}
		options.MasterName = ""
	}

	err := redis.SetInstance(s.ctx, ServiceName, options)
	if err != nil {
		log.Errorf(context.TODO(), "redis service start failed, err: %v", err)
		return
	}

	// redis 是关键依赖路径，如果没有则直接报错
	_, err = redis.Ping(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "redis service start failed, err: %v", err)
		return
	}

	// 初始化 SchemaProvider
	s.initSchemaProvider(s.ctx)
}

// initSchemaProvider 根据配置初始化 SchemaProvider
func (s *Service) initSchemaProvider(ctx context.Context) {
	if s.providerManager != nil {
		_ = s.providerManager.Close()
	}

	var redisClient goRedis.UniversalClient
	if SchemaProviderType == "redis" {
		client := redis.Client()
		if client == nil {
			log.Errorf(ctx, "Failed to get redis client for SchemaProvider: client is nil")
			return
		}
		redisClient = client
	}

	pm, err := relation.NewProviderManager(ctx, nil, SchemaProviderType, redisClient, relation.DefaultStaticProviderConfig())
	if err != nil {
		log.Errorf(ctx, "Failed to init SchemaProvider: %v", err)
		return
	}
	s.providerManager = pm

	v1beta1.InitSchemaProvider(s.providerManager.GetProvider())
	log.Infof(ctx, "SchemaProvider initialized with type: %s", SchemaProviderType)
}

func (s *Service) Wait() {
	redis.Wait()
}

func (s *Service) Close() {
	if s.providerManager != nil {
		s.providerManager.Close()
	}
	redis.Close()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}
