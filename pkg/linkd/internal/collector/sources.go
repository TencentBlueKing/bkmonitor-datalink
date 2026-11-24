// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"errors"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
)

// Releases 读取来源发布指针与不可变配置；eventsource.Service 实现该接口。
type Releases interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// ServiceFactory 根据已发布配置构造执行服务；装配层负责连接复用和关闭。
type ServiceFactory func(context.Context, eventsource.Release) (*Service, error)

// PublishedSources 将已发布 EventSource 转为来源插件绑定，不使用编辑中的 Spec。
type PublishedSources struct {
	releases Releases
	factory  ServiceFactory
}

// NewPublishedSources 复用来源发布服务和进程级连接装配。
func NewPublishedSources(releases Releases, factory ServiceFactory) *PublishedSources {
	return &PublishedSources{releases: releases, factory: factory}
}

// Replay 使用持久请求指定的不可变发布装配投递目标，再执行原请求。
// 调用方必须先取得来源执行授权并加载首次输入；此方法不提供调度、抢占或管理鉴权。
// 不读取当前发布指针，也不从新配置覆盖原请求的插件、凭据和游标。
func (s *PublishedSources) Replay(ctx context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
	release, err := s.releases.GetRelease(ctx, request.SourceID, request.SourceVersion)
	if err != nil {
		return pluginruntime.Result{}, fmt.Errorf("read replay source release: %w", err)
	}
	// 来源历史配置是投递权限边界，不能用其他租户的发布处理持久请求。
	authorized := request.TenantID != "" && release.Spec.RelatedTenantID == request.TenantID
	if collection := release.Spec.Collection; collection != nil && release.Spec.RelatedTenantID == "" {
		for _, binding := range collection.TenantBindings {
			if binding.Enabled && binding.TenantID == request.TenantID {
				authorized = true
				break
			}
		}
	}
	if !authorized {
		return pluginruntime.Result{}, ErrSourceUnauthorized
	}
	service, err := s.factory(ctx, release)
	if err != nil {
		return pluginruntime.Result{}, fmt.Errorf("assemble replay source: %w", err)
	}
	return service.Run(ctx, request)
}

// Read 返回单租户来源的调度绑定；共享接入使用 Lookup 按 Secret 选择租户。
func (s *PublishedSources) Read(ctx context.Context, id string) (SourceBinding, error) {
	bindings, err := s.bindings(ctx, id)
	if err != nil {
		return SourceBinding{}, err
	}
	if len(bindings) != 1 {
		return SourceBinding{}, fmt.Errorf("scheduled collection requires one tenant binding")
	}
	return bindings[0], nil
}

func (s *PublishedSources) bindings(ctx context.Context, id string) ([]SourceBinding, error) {
	record, err := s.releases.Get(ctx, id)
	if errors.Is(err, eventsource.ErrNotFound) {
		return nil, ErrSourceUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("read collector source: %w", err)
	}
	if record.Deleted || record.Published <= 0 {
		return nil, ErrSourceUnauthorized
	}
	release, err := s.releases.GetRelease(ctx, id, record.Published)
	if err != nil {
		return nil, fmt.Errorf("read collector source release: %w", err)
	}
	spec := release.Spec
	if release.Deleted || spec.Collection == nil || !spec.Enabled {
		return nil, ErrSourceUnauthorized
	}
	if spec.RelatedTenantID == "" && len(spec.Collection.TenantBindings) == 0 {
		return nil, fmt.Errorf("collector source requires explicit tenant binding")
	}
	service, err := s.factory(ctx, release)
	if err != nil {
		return nil, fmt.Errorf("assemble collector source: %w", err)
	}
	return collectionBindings(spec, release.Version, service), nil
}

// collectionBindings 统一正式接收与管理预览的插件上下文；启停策略由各入口决定。
func collectionBindings(spec config.EventSource, version int64, service *Service) []SourceBinding {
	collection := spec.Collection
	sourceName := collection.SourceName
	if sourceName == "" {
		sourceName = spec.EventSourceID
	}
	base := SourceBinding{Enabled: true, Secret: collection.Secret, Service: service, Request: pluginruntime.Request{
		SourceID: spec.EventSourceID, TenantID: spec.RelatedTenantID, SourceVersion: version,
		SourceName: sourceName, Plugin: collection.Plugin, Mode: collection.Mode,
		Timezone: collection.Timezone, State: collection.InitialState,
		PluginSourceID: collection.PluginSourceID,
		Configs:        collection.Configs, IntervalMinutes: collection.IntervalMinutes, RecoveryIntervalMinutes: collection.RecoveryIntervalMinutes,
	}}
	if spec.RelatedTenantID != "" {
		return []SourceBinding{base}
	}
	bindings := make([]SourceBinding, 0, len(collection.TenantBindings))
	for _, tenant := range collection.TenantBindings {
		binding := base
		binding.Enabled, binding.Secret = tenant.Enabled, tenant.Secret
		binding.Request.TenantID = tenant.TenantID
		binding.Request.Configs = tenant.Configs
		binding.Request.PluginSourceID = tenant.PluginSourceID
		if tenant.SourceName != "" {
			binding.Request.SourceName = tenant.SourceName
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

// Lookup 仅暴露 push 来源给 HTTP 接收；pull 来源不可通过接收接口触发执行。
func (s *PublishedSources) Lookup(ctx context.Context, id string) ([]SourceBinding, error) {
	bindings, err := s.bindings(ctx, id)
	if errors.Is(err, ErrSourceUnauthorized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 || bindings[0].Request.Mode != "push" {
		return nil, nil
	}
	return bindings, nil
}
