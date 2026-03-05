// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// RedisKeyPrefix Redis Key 前缀
	RedisKeyPrefix = "bkmonitorv3:entity"

	// 实体类型
	KindResourceDefinition = "ResourceDefinition"
	KindRelationDefinition = "RelationDefinition"

	NamespaceAll = "__all__"
)

// RedisSchemaProvider Redis 实现的 SchemaProvider
type RedisSchemaProvider struct {
	client redis.UniversalClient

	// 本地缓存
	// 外层 key: namespace (空的映射到 __all__)
	// 内层 key: 资源/关系名称
	resourceDefinitions map[string]map[string]*ResourceDefinition
	relationDefinitions map[string]map[string]*RelationDefinition

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRedisSchemaProvider 创建 RedisSchemaProvider
func NewRedisSchemaProvider(ctx context.Context, client redis.UniversalClient) (*RedisSchemaProvider, error) {
	ctx, cancel := context.WithCancel(ctx)

	provider := &RedisSchemaProvider{
		client:              client,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
		ctx:                 ctx,
		cancel:              cancel,
	}

	// 启动时全量加载
	if err := provider.loadAllEntities(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load entities: %w", err)
	}

	// 启动 Pub/Sub 订阅
	provider.wg.Add(1)
	go provider.subscribeEntities()

	logger.Infof("[schema_provider] RedisSchemaProvider initialized successfully")

	return provider, nil
}

// normalizeNamespace 规范化 namespace，空的映射到 __all__
func (rsp *RedisSchemaProvider) normalizeNamespace(namespace string) string {
	if namespace == "" {
		return NamespaceAll
	}
	return namespace
}

// GetResourceDefinition 获取资源定义
func (rsp *RedisSchemaProvider) GetResourceDefinition(namespace, resourceType string) (*ResourceDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := rsp.normalizeNamespace(namespace)

	// 先从指定 namespace 查找
	if nsMap, ok := rsp.resourceDefinitions[ns]; ok {
		if def, ok := nsMap[resourceType]; ok {
			return def, nil
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if allMap, ok := rsp.resourceDefinitions[NamespaceAll]; ok {
		if def, ok := allMap[resourceType]; ok {
			return def, nil
		}
	}

	return nil, fmt.Errorf("resource definition not found: namespace=%s, type=%s", namespace, resourceType)
}

func (rsp *RedisSchemaProvider) buildBidirectionalRelationKey(fromResource, toResource string) string {
	resources := []string{fromResource, toResource}
	sort.Strings(resources)
	return fmt.Sprintf("%s_with_%s", resources[0], resources[1])
}

func (rsp *RedisSchemaProvider) buildDirectionalRelationKey(fromResource, toResource string) string {
	return fmt.Sprintf("%s_to_%s", fromResource, toResource)
}

// GetRelationDefinition 获取关联
// 返回值：
//   - (*RelationDefinition, true): 找到了关系定义
//   - (nil, false): 未找到
func (rsp *RedisSchemaProvider) GetRelationDefinition(namespace, fromResource, toResource string, relationType RelationType) (*RelationDefinition, bool) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := rsp.normalizeNamespace(namespace)

	findInMap := func(nsMap map[string]*RelationDefinition) (*RelationDefinition, bool) {
		switch relationType {
		case RelationTypeDirectional:
			// 只查找单向关系
			directionalKey := rsp.buildDirectionalRelationKey(fromResource, toResource)
			if def, ok := nsMap[directionalKey]; ok {
				return def, true
			}
			return nil, false
		case RelationTypeBidirectional:
			// 只查找双向关系
			bidirectionalKey := rsp.buildBidirectionalRelationKey(fromResource, toResource)
			if def, ok := nsMap[bidirectionalKey]; ok {
				return def, true
			}
			return nil, false
		default:
			return nil, false
		}
	}

	// 先从指定 namespace 查找
	if nsMap, ok := rsp.relationDefinitions[ns]; ok {
		if def, found := findInMap(nsMap); found {
			return def, true
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if nsMap, ok := rsp.relationDefinitions[NamespaceAll]; ok {
		if def, found := findInMap(nsMap); found {
			return def, true
		}
	}

	return nil, false
}

// ListRelationDefinitions 列出指定 namespace 下的所有关系定义
// 会合并指定 namespace 和 __all__ namespace 的定义
func (rsp *RedisSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := rsp.normalizeNamespace(namespace)
	definitions := make([]*RelationDefinition, 0)
	seen := make(map[string]struct{}) // 用于去重

	// 从指定 namespace 获取
	if nsMap, ok := rsp.relationDefinitions[ns]; ok {
		for key, def := range nsMap {
			definitions = append(definitions, def)
			seen[key] = struct{}{}
		}
	}

	// 如果不是 __all__，还需要从 __all__ 获取（合并）
	if allMap, ok := rsp.relationDefinitions[NamespaceAll]; ok {
		for key, def := range allMap {
			// 跳过已存在的（指定 namespace 优先）
			if _, exists := seen[key]; !exists {
				definitions = append(definitions, def)
			}
		}
	}

	return definitions, nil
}

// loadAllEntities 启动时全量加载所有实体
func (rsp *RedisSchemaProvider) loadAllEntities() error {
	// 加载资源定义
	if err := rsp.loadEntitiesByKind(KindResourceDefinition); err != nil {
		return fmt.Errorf("failed to load resource definitions: %w", err)
	}

	// 加载关系定义
	if err := rsp.loadEntitiesByKind(KindRelationDefinition); err != nil {
		return fmt.Errorf("failed to load relation definitions: %w", err)
	}

	// 统计数量
	resourceCount := 0
	for _, nsMap := range rsp.resourceDefinitions {
		resourceCount += len(nsMap)
	}
	relationCount := 0
	for _, nsMap := range rsp.relationDefinitions {
		relationCount += len(nsMap)
	}

	logger.Infof("[schema_provider] loaded %d resource definitions, %d relation definitions",
		resourceCount, relationCount)

	return nil
}

// loadEntitiesByKind 按 kind 加载实体
// Redis 结构: redisKey -> namespace -> {name: jsonData, name2: jsonData2, ...}
func (rsp *RedisSchemaProvider) loadEntitiesByKind(kind string) error {
	redisKey := fmt.Sprintf("%s:%s", RedisKeyPrefix, kind)

	result, err := rsp.client.HGetAll(rsp.ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("failed to hgetall %s: %w", redisKey, err)
	}

	// 解析并存储
	count := 0
	for namespace, entitiesJson := range result {
		// 解析 namespace 下的所有实体
		var entities map[string]json.RawMessage
		if err := json.Unmarshal([]byte(entitiesJson), &entities); err != nil {
			logger.Warnf("[schema_provider] failed to unmarshal entities for namespace %s: %v", namespace, err)
			continue
		}

		// 遍历每个实体
		for name, jsonData := range entities {
			if err := rsp.loadEntityByKind(kind, namespace, name, string(jsonData)); err != nil {
				logger.Warnf("[schema_provider] failed to load %s %s:%s: %v", kind, namespace, name, err)
				continue
			}
			count++
		}
	}

	logger.Debugf("[schema_provider] loaded %d entities of kind %s", count, kind)

	return nil
}

// loadEntityByKind 按 kind 加载单个实体
// jsonData 直接是 ResourceDefinition 或 RelationDefinition 的 JSON 格式
func (rsp *RedisSchemaProvider) loadEntityByKind(kind, namespace, name, jsonData string) error {
	normalizedNs := rsp.normalizeNamespace(namespace)

	switch kind {
	case KindResourceDefinition:
		var def ResourceDefinition
		if err := json.Unmarshal([]byte(jsonData), &def); err != nil {
			return fmt.Errorf("failed to unmarshal ResourceDefinition: %w", err)
		}

		rsp.mu.Lock()
		if _, ok := rsp.resourceDefinitions[normalizedNs]; !ok {
			rsp.resourceDefinitions[normalizedNs] = make(map[string]*ResourceDefinition)
		}
		rsp.resourceDefinitions[normalizedNs][name] = &def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded resource definition: ns=%s, name=%s", normalizedNs, name)

	case KindRelationDefinition:
		var def RelationDefinition
		if err := json.Unmarshal([]byte(jsonData), &def); err != nil {
			return fmt.Errorf("failed to unmarshal RelationDefinition: %w", err)
		}

		// 根据关系类型使用不同的 key
		var relationKey string
		if def.IsDirectional {
			// 单向关系：使用 from_to_to 格式
			relationKey = rsp.buildDirectionalRelationKey(def.FromResource, def.ToResource)
		} else {
			// 双向关系：按字母序排序
			relationKey = rsp.buildBidirectionalRelationKey(def.FromResource, def.ToResource)
		}

		rsp.mu.Lock()
		if _, ok := rsp.relationDefinitions[normalizedNs]; !ok {
			rsp.relationDefinitions[normalizedNs] = make(map[string]*RelationDefinition)
		}
		rsp.relationDefinitions[normalizedNs][relationKey] = &def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded relation definition: ns=%s, key=%s", normalizedNs, relationKey)
	}

	return nil
}

type MsgPayload struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name,omitempty"` // Deprecated: 保留用于向后兼容，新消息不再包含此字段
	Kind      string `json:"kind"`           // KindResourceDefinition or KindRelationDefinition
}

func (m *MsgPayload) IsEmpty() bool {
	return m.Namespace == ""
}

// subscribeEntities 订阅实体变更
func (rsp *RedisSchemaProvider) subscribeEntities() {
	defer rsp.wg.Done()

	// 订阅 ResourceDefinition 和 RelationDefinition 的 channels
	channels := []string{
		fmt.Sprintf("%s:%s:channel", RedisKeyPrefix, KindResourceDefinition),
		fmt.Sprintf("%s:%s:channel", RedisKeyPrefix, KindRelationDefinition),
	}

	logger.Infof("[schema_provider] subscribing to channels: %v", channels)

	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff

	for {
		select {
		case <-rsp.ctx.Done():
			logger.Infof("[schema_provider] subscription stopped")
			return
		default:
		}

		pubsub := rsp.client.Subscribe(rsp.ctx, channels...)

		// subscribeLoop 返回 true 表示正常退出（ctx done），false 表示需要重连
		normalExit := func() bool {
			defer pubsub.Close()

			ch := pubsub.Channel()
			for {
				select {
				case <-rsp.ctx.Done():
					return true
				case msg, ok := <-ch:
					if !ok {
						logger.Warnf("[schema_provider] pubsub channel closed, reconnecting...")
						return false
					}
					if msg == nil {
						continue
					}

					backoff = initialBackoff

					var payload MsgPayload
					if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
						logger.Warnf("[schema_provider] invalid payload format: %s, err: %v", msg.Payload, err)
						continue
					}

					if payload.IsEmpty() {
						logger.Warnf("[schema_provider] invalid payload format: %s", msg.Payload)
						continue
					}

					logger.Infof("[schema_provider] received update: kind=%s namespace=%s", payload.Kind, payload.Namespace)
					if err := rsp.reloadNamespace(payload.Kind, payload.Namespace); err != nil {
						logger.Errorf("[schema_provider] failed to reload namespace: %v", err)
					}
				}
			}
		}()

		if normalExit {
			return
		}

		logger.Infof("[schema_provider] waiting %v before reconnecting...", backoff)
		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// reloadNamespace 按 namespace 全量重建该 kind 的本地缓存
// 与 metadata 端的 _rebuild_redis_cache 保持一致：每次变更都是按 namespace 全量覆盖
// Redis 结构: redisKey -> namespace -> {name: jsonData, ...}
func (rsp *RedisSchemaProvider) reloadNamespace(kind, namespace string) error {
	schemaKey := fmt.Sprintf("%s:%s", RedisKeyPrefix, kind)
	normalizedNs := rsp.normalizeNamespace(namespace)

	// HGET 获取 namespace 下的所有实体
	entitiesJson, err := rsp.client.HGet(rsp.ctx, schemaKey, namespace).Result()
	if errors.Is(err, redis.Nil) {
		// namespace 已不存在，清空该 namespace 的本地缓存
		rsp.clearNamespaceCache(kind, normalizedNs)
		logger.Infof("[schema_provider] cleared namespace cache: kind=%s, ns=%s (namespace not found in redis)", kind, normalizedNs)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to hget: %w", err)
	}

	// 解析 namespace 下的所有实体
	var entities map[string]json.RawMessage
	if err := json.Unmarshal([]byte(entitiesJson), &entities); err != nil {
		return fmt.Errorf("failed to unmarshal entities: %w", err)
	}

	// 全量重建该 namespace 的缓存
	switch kind {
	case KindResourceDefinition:
		newMap := make(map[string]*ResourceDefinition, len(entities))
		for name, jsonData := range entities {
			var def ResourceDefinition
			if err := json.Unmarshal(jsonData, &def); err != nil {
				logger.Warnf("[schema_provider] failed to unmarshal ResourceDefinition %s:%s: %v", normalizedNs, name, err)
				continue
			}
			newMap[name] = &def
		}
		rsp.mu.Lock()
		rsp.resourceDefinitions[normalizedNs] = newMap
		rsp.mu.Unlock()

	case KindRelationDefinition:
		newMap := make(map[string]*RelationDefinition, len(entities))
		for _, jsonData := range entities {
			var def RelationDefinition
			if err := json.Unmarshal(jsonData, &def); err != nil {
				logger.Warnf("[schema_provider] failed to unmarshal RelationDefinition in ns=%s: %v", normalizedNs, err)
				continue
			}
			var relationKey string
			if def.IsDirectional {
				relationKey = rsp.buildDirectionalRelationKey(def.FromResource, def.ToResource)
			} else {
				relationKey = rsp.buildBidirectionalRelationKey(def.FromResource, def.ToResource)
			}
			newMap[relationKey] = &def
		}
		rsp.mu.Lock()
		rsp.relationDefinitions[normalizedNs] = newMap
		rsp.mu.Unlock()
	}

	logger.Infof("[schema_provider] reloaded namespace cache: kind=%s, ns=%s, count=%d", kind, normalizedNs, len(entities))
	return nil
}

// clearNamespaceCache 清空指定 kind + namespace 的本地缓存
func (rsp *RedisSchemaProvider) clearNamespaceCache(kind, normalizedNs string) {
	rsp.mu.Lock()
	defer rsp.mu.Unlock()

	switch kind {
	case KindResourceDefinition:
		delete(rsp.resourceDefinitions, normalizedNs)
	case KindRelationDefinition:
		delete(rsp.relationDefinitions, normalizedNs)
	}
}

// Close 关闭 provider
func (rsp *RedisSchemaProvider) Close() error {
	rsp.cancel()
	rsp.wg.Wait()
	logger.Infof("[schema_provider] RedisSchemaProvider closed")
	return nil
}
