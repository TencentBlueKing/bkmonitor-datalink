// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package deliverysource 从部署级 KAC 插件解析出口；来源版本仅作业务溯源，不参与路由或凭据选择。
package deliverysource

import (
	"context"
	"errors"

	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
)

// ErrUnavailable 表示插件未启用或请求身份不合法，不尝试来源配置和其他目标兜底。
var ErrUnavailable = errors.New("global KAC plugin unavailable")

// TargetID 是内置兼容插件唯一的稳定目标身份，不因租户或来源变化。
const TargetID = "kac"

// Resolver 持有独立的公共出口配置，租户始终来自本次任务。
type Resolver struct{ config config.KACPluginConfig }

// New 验证并复制全局插件配置，不读取 EventSource 或外部服务。
func New(plugins config.PluginsConfig) (*Resolver, error) {
	if err := plugins.Validate(); err != nil {
		return nil, err
	}
	if !plugins.KACEnabled() {
		return nil, ErrUnavailable
	}
	return &Resolver{config: plugins.KAC.WithDefaults()}, nil
}

func validScope(ctx context.Context, tenant, source string, version int64, target string) error {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || domain.ValidateIdentityPart("source", source, 32) != nil || version < 1 || version >= 1<<53 || target != TargetID {
		return ErrUnavailable
	}
	return ctx.Err()
}

// ResolveProjection 只验证业务作用域；实际 ES 连接在组装写入器时注入，不经过 HTTP 状态接收端。
func (r *Resolver) ResolveProjection(ctx context.Context, tenant, source string, version int64, target string) (projection.Destination, error) {
	if err := validScope(ctx, tenant, source, version, target); err != nil {
		return projection.Destination{}, err
	}
	return projection.Destination{TargetID: TargetID}, nil
}

// ResolveAction 所有租户和来源共用部署级入口与凭据；任务中仍显式携带租户身份。
func (r *Resolver) ResolveAction(ctx context.Context, tenant, source string, version int64, target string) (actiondelivery.Destination, error) {
	if err := validScope(ctx, tenant, source, version, target); err != nil {
		return actiondelivery.Destination{}, err
	}
	return actiondelivery.Destination{Endpoint: r.config.ActionEndpoint, InternalToken: r.config.InternalToken}, nil
}
