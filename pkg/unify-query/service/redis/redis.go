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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
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

// ClientOptions 返回运行服务与配置命令共用的 Redis 连接配置。
func ClientOptions() *goRedis.UniversalOptions {
	options := &goRedis.UniversalOptions{
		MasterName:       MasterName,
		DB:               DataBase,
		Password:         Password,
		SentinelPassword: SentinelPassword,
		DialTimeout:      DialTimeout,
		ReadTimeout:      ReadTimeout,
	}
	if Mode == "sentinel" {
		options.Addrs = SentinelAddress
	} else {
		options.Addrs = []string{fmt.Sprintf("%s:%d", Host, Port)}
		options.MasterName = ""
	}
	return options
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

	// 关联关系定义不依赖 Redis 可用：Redis 不可用时以内置定义启动，覆盖层由订阅与对账重试。
	defer s.initSchemaProvider(s.ctx)

	err := redis.SetInstance(s.ctx, KVBasePath, ServiceName, ClientOptions())
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
}

// initSchemaProvider 初始化以内置定义作底层、Redis 作覆盖层的 SchemaProvider。
// Redis 客户端缺失时只有内置一层，覆盖层状态见 /relation_schema_print。
func (s *Service) initSchemaProvider(ctx context.Context) {
	if s.providerManager != nil {
		_ = s.providerManager.Close()
	}

	redisClient := redis.Client()
	if redisClient == nil {
		log.Errorf(ctx, "redis client is nil, relation SchemaProvider serves builtin definitions only")
	}
	s.providerManager = relation.NewProviderManager(ctx, nil, redisClient, relation.DefaultStaticProviderConfig())

	v1beta1.InitSchemaProvider(s.providerManager.GetProvider())
	v1beta3.InitSchemaProvider(s.providerManager.GetProvider())
	log.Infof(ctx, "relation SchemaProvider initialized: builtin base with Redis overlay")
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
