// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	unifyRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/go-redis/redis/v8"
)

const (
	// Redis Key 前缀（与 bk-monitor-worker 保持一致）
	DefaultRedisKeyPrefix = "bkmonitorv3:entity"

	// Pub/Sub 通道后缀
	DefaultRedisPubSubChannelSuffix = ":channel"

	// 实体 Kind 名称（与 bk-monitor-worker 保持一致）
	KindResourceDef = "ResourceDefinition"
	KindRelationDef = "RelationDefinition"

	// Redis Hash key（与 bk-monitor-worker 保持一致）
	// 结构: bkmonitorv3:entity:{Kind} -> namespace -> {name: JSON, ...}
	DefaultRedisKeyPrefixResourceDef = DefaultRedisKeyPrefix + ":" + KindResourceDef
	DefaultRedisKeyPrefixRelationDef = DefaultRedisKeyPrefix + ":" + KindRelationDef

	// Pub/Sub 通道名称
	DefaultRedisPubSubChannelResourceDef = DefaultRedisKeyPrefixResourceDef + DefaultRedisPubSubChannelSuffix
	DefaultRedisPubSubChannelRelationDef = DefaultRedisKeyPrefixRelationDef + DefaultRedisPubSubChannelSuffix

	// 内部默认值
	defaultRedisReconnectInterval = 5 * time.Second
	defaultRedisReconnectMaxRetry = 10
)

// RedisSchemaProviderConfig Redis Schema 提供器配置
type RedisSchemaProviderConfig struct {
	ReconnectInterval time.Duration
	ReconnectMaxRetry int
	ReloadOnStart     bool
}

// DefaultRedisSchemaProviderConfig 返回默认配置
func DefaultRedisSchemaProviderConfig() *RedisSchemaProviderConfig {
	return &RedisSchemaProviderConfig{
		ReconnectInterval: defaultRedisReconnectInterval,
		ReconnectMaxRetry: defaultRedisReconnectMaxRetry,
		ReloadOnStart:     true,
	}
}

// RedisSchemaProvider Redis Schema 提供器
// 从 Redis 动态加载资源和关联定义，支持 Pub/Sub 热更新
//
// Redis 数据结构（与 bk-monitor-worker 保持一致）:
//   - key:   bkmonitorv3:entity:ResourceDefinition
//   - field: {namespace}
//   - value: {"name1": {JSON}, "name2": {JSON}, ...}
type RedisSchemaProvider struct {
	client redis.UniversalClient
	config *RedisSchemaProviderConfig

	// 外层 key: namespace, 内层 key: resource/relation name
	resourceDefinitions map[string]map[string]*ResourceDefinition
	relationDefinitions map[string]map[string]*RelationDefinition
	mu                  sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// RedisSchemaProviderOption Redis Schema 提供器配置选项
type RedisSchemaProviderOption func(*RedisSchemaProviderConfig)

// WithReloadOnStart 设置启动时是否重新加载所有数据
func WithReloadOnStart(reload bool) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.ReloadOnStart = reload
	}
}

// WithReconnectConfig 设置重连配置
func WithReconnectConfig(interval time.Duration, maxRetry int) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.ReconnectInterval = interval
		config.ReconnectMaxRetry = maxRetry
	}
}

// NewRedisSchemaProvider 创建 Redis Schema 提供器（使用自定义客户端）
func NewRedisSchemaProvider(client redis.UniversalClient, opts ...RedisSchemaProviderOption) (*RedisSchemaProvider, error) {
	config := DefaultRedisSchemaProviderConfig()
	for _, opt := range opts {
		opt(config)
	}

	ctx, cancel := context.WithCancel(context.Background())
	provider := &RedisSchemaProvider{
		client:              client,
		config:              config,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
		ctx:                 ctx,
		cancel:              cancel,
	}

	if config.ReloadOnStart {
		if err := provider.loadAllEntities(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to load entities: %w", err)
		}
	}

	provider.wg.Add(1)
	go provider.subscribeEntities()

	return provider, nil
}

// NewRedisSchemaProviderWithGlobalClient 创建 Redis Schema 提供器（使用全局客户端）
func NewRedisSchemaProviderWithGlobalClient(opts ...RedisSchemaProviderOption) (*RedisSchemaProvider, error) {
	client := unifyRedis.Client()
	if client == nil {
		return nil, fmt.Errorf("global redis client is not initialized, please call redis.SetInstance first")
	}
	return NewRedisSchemaProvider(client, opts...)
}

// Close 关闭提供器
func (rsp *RedisSchemaProvider) Close() error {
	rsp.cancel()
	rsp.wg.Wait()
	return nil
}

// loadAllEntities 启动时全量加载所有实体
func (rsp *RedisSchemaProvider) loadAllEntities() error {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_all_entities")
	defer span.End(&err)

	if err = rsp.loadEntitiesByKind(ctx, KindResourceDef); err != nil {
		return fmt.Errorf("failed to load resource definitions: %w", err)
	}
	if err = rsp.loadEntitiesByKind(ctx, KindRelationDef); err != nil {
		return fmt.Errorf("failed to load relation definitions: %w", err)
	}

	resourceCount := 0
	for _, nsMap := range rsp.resourceDefinitions {
		resourceCount += len(nsMap)
	}
	relationCount := 0
	for _, nsMap := range rsp.relationDefinitions {
		relationCount += len(nsMap)
	}

	log.Infof(ctx, "loaded %d resource definitions, %d relation definitions", resourceCount, relationCount)
	return nil
}

// loadEntitiesByKind 按 Kind 全量加载实体
// Redis 结构: {prefix}:{kind} -> namespace -> {name: JSON, ...}
func (rsp *RedisSchemaProvider) loadEntitiesByKind(ctx context.Context, kind string) error {
	redisKey := DefaultRedisKeyPrefix + ":" + kind

	result, err := rsp.client.HGetAll(ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("failed to hgetall %s: %w", redisKey, err)
	}

	for namespace, entitiesJSON := range result {
		var entities map[string]json.RawMessage
		if unmarshalErr := json.Unmarshal([]byte(entitiesJSON), &entities); unmarshalErr != nil {
			log.Warnf(ctx, "failed to unmarshal entities for namespace %s: %v", namespace, unmarshalErr)
			continue
		}
		for name, jsonData := range entities {
			if loadErr := rsp.loadEntityByKind(kind, namespace, name, string(jsonData)); loadErr != nil {
				log.Warnf(ctx, "failed to load %s %s:%s: %v", kind, namespace, name, loadErr)
			}
		}
	}
	return nil
}

// loadEntityByKind 加载单个实体到缓存
func (rsp *RedisSchemaProvider) loadEntityByKind(kind, namespace, name, jsonData string) error {
	switch kind {
	case KindResourceDef:
		var def ResourceDefinition
		if err := json.Unmarshal([]byte(jsonData), &def); err != nil {
			return fmt.Errorf("failed to unmarshal ResourceDefinition: %w", err)
		}
		rsp.mu.Lock()
		if _, ok := rsp.resourceDefinitions[namespace]; !ok {
			rsp.resourceDefinitions[namespace] = make(map[string]*ResourceDefinition)
		}
		rsp.resourceDefinitions[namespace][name] = &def
		rsp.mu.Unlock()

	case KindRelationDef:
		var def RelationDefinition
		if err := json.Unmarshal([]byte(jsonData), &def); err != nil {
			return fmt.Errorf("failed to unmarshal RelationDefinition: %w", err)
		}
		rsp.mu.Lock()
		if _, ok := rsp.relationDefinitions[namespace]; !ok {
			rsp.relationDefinitions[namespace] = make(map[string]*RelationDefinition)
		}
		rsp.relationDefinitions[namespace][name] = &def
		rsp.mu.Unlock()
	}
	return nil
}

// reloadNamespace 按 namespace 全量重建该 kind 的本地缓存（与 bk-monitor-worker 保持一致）
func (rsp *RedisSchemaProvider) reloadNamespace(ctx context.Context, kind, namespace string) error {
	redisKey := DefaultRedisKeyPrefix + ":" + kind

	entitiesJSON, err := rsp.client.HGet(ctx, redisKey, namespace).Result()
	if errors.Is(err, redis.Nil) {
		// namespace 已不存在，清空缓存
		rsp.mu.Lock()
		switch kind {
		case KindResourceDef:
			delete(rsp.resourceDefinitions, namespace)
		case KindRelationDef:
			delete(rsp.relationDefinitions, namespace)
		}
		rsp.mu.Unlock()
		log.Infof(ctx, "cleared namespace cache: kind=%s, ns=%s", kind, namespace)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to hget: %w", err)
	}

	var entities map[string]json.RawMessage
	if err = json.Unmarshal([]byte(entitiesJSON), &entities); err != nil {
		return fmt.Errorf("failed to unmarshal entities: %w", err)
	}

	switch kind {
	case KindResourceDef:
		newMap := make(map[string]*ResourceDefinition, len(entities))
		for name, jsonData := range entities {
			var def ResourceDefinition
			if unmarshalErr := json.Unmarshal(jsonData, &def); unmarshalErr != nil {
				log.Warnf(ctx, "failed to unmarshal ResourceDefinition %s:%s: %v", namespace, name, unmarshalErr)
				continue
			}
			newMap[name] = &def
		}
		rsp.mu.Lock()
		rsp.resourceDefinitions[namespace] = newMap
		rsp.mu.Unlock()

	case KindRelationDef:
		newMap := make(map[string]*RelationDefinition, len(entities))
		for name, jsonData := range entities {
			var def RelationDefinition
			if unmarshalErr := json.Unmarshal(jsonData, &def); unmarshalErr != nil {
				log.Warnf(ctx, "failed to unmarshal RelationDefinition %s:%s: %v", namespace, name, unmarshalErr)
				continue
			}
			newMap[name] = &def
		}
		rsp.mu.Lock()
		rsp.relationDefinitions[namespace] = newMap
		rsp.mu.Unlock()
	}

	log.Infof(ctx, "reloaded namespace cache: kind=%s, ns=%s, count=%d", kind, namespace, len(entities))
	return nil
}

// MsgPayload Pub/Sub 消息体（与 bk-monitor-worker 保持一致）
type MsgPayload struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
}

// subscribeEntities 订阅实体变更通知
func (rsp *RedisSchemaProvider) subscribeEntities() {
	defer rsp.wg.Done()

	channels := []string{
		DefaultRedisPubSubChannelResourceDef,
		DefaultRedisPubSubChannelRelationDef,
	}

	retryCount := 0
	for {
		select {
		case <-rsp.ctx.Done():
			return
		default:
		}

		pubsub := rsp.client.Subscribe(rsp.ctx, channels...)
		log.Infof(rsp.ctx, "subscribed to entity channels: %v", channels)

		done := make(chan struct{})
		go func() {
			defer close(done)
			ch := pubsub.Channel()
			for {
				select {
				case <-rsp.ctx.Done():
					return
				case msg := <-ch:
					if msg == nil {
						return
					}

					var payload MsgPayload
					if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
						log.Warnf(rsp.ctx, "invalid pubsub payload: %s, err: %v", msg.Payload, err)
						continue
					}
					if payload.Namespace == "" || payload.Kind == "" {
						log.Warnf(rsp.ctx, "empty namespace or kind in payload: %s", msg.Payload)
						continue
					}

					log.Infof(rsp.ctx, "received entity update: kind=%s namespace=%s", payload.Kind, payload.Namespace)
					if err := rsp.reloadNamespace(rsp.ctx, payload.Kind, payload.Namespace); err != nil {
						log.Errorf(rsp.ctx, "failed to reload namespace %s:%s: %v", payload.Kind, payload.Namespace, err)
					}
				}
			}
		}()

		select {
		case <-rsp.ctx.Done():
			pubsub.Close()
			<-done
			return
		case <-done:
			pubsub.Close()
		}

		retryCount++
		if retryCount > rsp.config.ReconnectMaxRetry {
			log.Errorf(rsp.ctx, "max retry count reached for entity subscription")
			return
		}

		log.Warnf(rsp.ctx, "entity subscription disconnected, retrying in %v (attempt %d/%d)",
			rsp.config.ReconnectInterval, retryCount, rsp.config.ReconnectMaxRetry)

		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(rsp.config.ReconnectInterval):
		}
	}
}

// GetResourceDefinition 获取资源定义
func (rsp *RedisSchemaProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.get_resource_definition")
	defer span.End(&err)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	if nsMap, ok := rsp.resourceDefinitions[namespace]; ok {
		if def, ok := nsMap[name]; ok {
			span.Set("cache.hit", true)
			return def, nil
		}
	}

	err = ErrResourceDefinitionNotFound
	span.Set("cache.hit", false)
	log.Debugf(ctx, "resource definition not found: namespace=%s, name=%s", namespace, name)
	return nil, err
}

// ListResourceDefinitions 列出指定命名空间下的所有资源定义
func (rsp *RedisSchemaProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]*ResourceDefinition, 0)
	if namespace == "" {
		for _, nsMap := range rsp.resourceDefinitions {
			for _, def := range nsMap {
				result = append(result, def)
			}
		}
		return result, nil
	}

	if nsMap, ok := rsp.resourceDefinitions[namespace]; ok {
		for _, def := range nsMap {
			result = append(result, def)
		}
	}
	return result, nil
}

// GetRelationDefinition 获取关联定义
func (rsp *RedisSchemaProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.get_relation_definition")
	defer span.End(&err)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	if nsMap, ok := rsp.relationDefinitions[namespace]; ok {
		if def, ok := nsMap[name]; ok {
			span.Set("cache.hit", true)
			return def, nil
		}
	}

	err = ErrRelationDefinitionNotFound
	span.Set("cache.hit", false)
	log.Debugf(ctx, "relation definition not found: namespace=%s, name=%s", namespace, name)
	return nil, err
}

// ListRelationDefinitions 列出指定命名空间下的所有关联定义
func (rsp *RedisSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]*RelationDefinition, 0)
	if namespace == "" {
		for _, nsMap := range rsp.relationDefinitions {
			for _, def := range nsMap {
				result = append(result, def)
			}
		}
		return result, nil
	}

	if nsMap, ok := rsp.relationDefinitions[namespace]; ok {
		for _, def := range nsMap {
			result = append(result, def)
		}
	}
	return result, nil
}

// GetResourcePrimaryKeys 获取资源类型的主键字段列表
func (rsp *RedisSchemaProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	for _, nsMap := range rsp.resourceDefinitions {
		for _, rd := range nsMap {
			if rd.ToResourceType() == resourceType {
				return rd.GetPrimaryKeys()
			}
		}
	}
	return []string{}
}

// GetRelationSchema 获取关联关系的 Schema
func (rsp *RedisSchemaProvider) GetRelationSchema(relationType RelationType) (*RelationSchema, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	for _, nsMap := range rsp.relationDefinitions {
		for _, rd := range nsMap {
			if rd.ToRelationType() == relationType {
				schema := rd.ToRelationSchema()
				return &schema, nil
			}
		}
	}
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出所有关联 Schema
func (rsp *RedisSchemaProvider) ListRelationSchemas() []RelationSchema {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]RelationSchema, 0)
	for _, nsMap := range rsp.relationDefinitions {
		for _, rd := range nsMap {
			result = append(result, rd.ToRelationSchema())
		}
	}
	return result
}

// Ensure RedisSchemaProvider implements SchemaProvider
var _ SchemaProvider = (*RedisSchemaProvider)(nil)
