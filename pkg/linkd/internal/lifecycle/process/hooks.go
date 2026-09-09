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
	"errors"
	"fmt"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kafkahook"
	"linkd/internal/lifecycle/strategyhook"
	"linkd/internal/redisclient"
	"linkd/internal/telemetry"
)

// openHooks 按来源 Release 装配有界实例列表，不预连外部输出服务。
// 每个任务独占客户端，发布切换先排空旧任务再关闭，避免影响其他来源。
func openHooks(configs []config.HookConfig, runtime *telemetry.Runtime) ([]lifecycle.NamedFinalHook, func() error, error) {
	return assembleHooks(configs, runtime, openHook)
}

// assembleHooks 的工厂参数允许验证部分初始化失败时的资源回收契约。
func assembleHooks(configs []config.HookConfig, runtime *telemetry.Runtime, factory func(config.HookConfig) (lifecycle.FinalHook, func() error, error)) ([]lifecycle.NamedFinalHook, func() error, error) {
	if err := config.ValidateHooks(configs); err != nil {
		return nil, nil, err
	}
	hooks := make([]lifecycle.NamedFinalHook, 0, len(configs))
	closers := make([]func() error, 0, len(configs))
	var once sync.Once
	var closeErr error
	closeAll := func() error {
		once.Do(func() {
			for i := len(closers) - 1; i >= 0; i-- {
				closeErr = errors.Join(closeErr, closers[i]())
			}
		})
		return closeErr
	}
	for _, spec := range configs {
		spec = spec.WithDefaults()
		hook, closeHook, err := factory(spec)
		if err != nil {
			return nil, nil, errors.Join(fmt.Errorf("hook %s initialization failed: %w", spec.Name, err), closeAll())
		}
		closers = append(closers, closeHook)
		named := lifecycle.NamedFinalHook{Name: spec.Name, Hook: hook}
		hooks = append(hooks, lifecycle.NamedFinalHook{Name: spec.Name, Hook: runtime.ObserveFinalHook(named)})
	}
	return hooks, closeAll, nil
}

func openHook(spec config.HookConfig) (lifecycle.FinalHook, func() error, error) {
	switch spec.Type {
	case config.HookTypeKafka:
		hook, err := kafkahook.New(spec.KafkaConfig())
		if err != nil {
			return nil, nil, err
		}
		return hook, func() error { hook.Close(); return nil }, nil
	case config.HookTypeActiveAlertByStrategy:
		options := spec.Config.Redis.ClientOptions()
		options.ContextTimeoutEnabled = true
		client, err := redisclient.New(options)
		if err != nil {
			return nil, nil, err
		}
		// go-redis 默认 socket deadline 不追随调用方 context；为毫秒级插件单独启用。
		// 专用构造入口负责在创建连接池前设置选项。
		hook, err := strategyhook.New(client, spec.Config.KeyPrefix, time.Duration(*spec.Config.TimeoutMilliseconds)*time.Millisecond)
		if err != nil {
			return nil, nil, errors.Join(err, client.Close())
		}
		return hook, client.Close, nil
	default:
		return nil, nil, fmt.Errorf("unregistered hook type")
	}
}
