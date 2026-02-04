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
func normalizeNamespace(namespace string) string {
	if namespace == "" {
		return NamespaceAll
	}
	return namespace
}

// GetResourceDefinition 获取资源定义
func (rsp *RedisSchemaProvider) GetResourceDefinition(namespace, resourceType string) (*ResourceDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := normalizeNamespace(namespace)

	// 先从指定 namespace 查找
	if nsMap, ok := rsp.resourceDefinitions[ns]; ok {
		if def, ok := nsMap[resourceType]; ok {
			return def, nil
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if ns != NamespaceAll {
		if allMap, ok := rsp.resourceDefinitions[NamespaceAll]; ok {
			if def, ok := allMap[resourceType]; ok {
				return def, nil
			}
		}
	}

	return nil, fmt.Errorf("resource definition not found: namespace=%s, type=%s", namespace, resourceType)
}

// buildRelationKey 构建双向关系的 key，按字母序排序
// 用于双向关系（IsBelongsTo=false）
func buildRelationKey(fromResource, toResource string) string {
	resources := []string{fromResource, toResource}
	sort.Strings(resources)
	return fmt.Sprintf("%s_with_%s", resources[0], resources[1])
}

// buildDirectionalRelationKey 构建单向关系的 key
// 用于单向关系（IsBelongsTo=true）
func buildDirectionalRelationKey(fromResource, toResource string) string {
	return fmt.Sprintf("%s_to_%s", fromResource, toResource)
}

// GetRelationDefinition 获取关系定义
// 会同时尝试查找单向关系（from_to_to）和双向关系（按字母序排序）
func (rsp *RedisSchemaProvider) GetRelationDefinition(namespace, fromResource, toResource string) (*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := normalizeNamespace(namespace)

	// 辅助函数：在指定 map 中查找关系定义
	findInMap := func(nsMap map[string]*RelationDefinition) *RelationDefinition {
		// 先尝试单向关系 key
		directionalKey := buildDirectionalRelationKey(fromResource, toResource)
		if def, ok := nsMap[directionalKey]; ok {
			return def
		}

		// 再尝试双向关系 key（按字母序排序）
		bidirectionalKey := buildRelationKey(fromResource, toResource)
		if def, ok := nsMap[bidirectionalKey]; ok {
			return def
		}

		return nil
	}

	// 先从指定 namespace 查找
	if nsMap, ok := rsp.relationDefinitions[ns]; ok {
		if def := findInMap(nsMap); def != nil {
			return def, nil
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if ns != NamespaceAll {
		if allMap, ok := rsp.relationDefinitions[NamespaceAll]; ok {
			if def := findInMap(allMap); def != nil {
				return def, nil
			}
		}
	}

	return nil, fmt.Errorf("relation definition not found: namespace=%s, from=%s, to=%s", namespace, fromResource, toResource)
}

// ListRelationDefinitions 列出指定 namespace 下的所有关系定义
// 会合并指定 namespace 和 __all__ namespace 的定义
func (rsp *RedisSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	ns := normalizeNamespace(namespace)
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
	if ns != NamespaceAll {
		if allMap, ok := rsp.relationDefinitions[NamespaceAll]; ok {
			for key, def := range allMap {
				// 跳过已存在的（指定 namespace 优先）
				if _, exists := seen[key]; !exists {
					definitions = append(definitions, def)
				}
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
func (rsp *RedisSchemaProvider) loadEntityByKind(kind, namespace, name, jsonData string) error {
	normalizedNs := normalizeNamespace(namespace)

	switch kind {
	case KindResourceDefinition:
		// 解析 metadata 和 spec 结构
		var rawData map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &rawData); err != nil {
			return fmt.Errorf("failed to unmarshal raw data: %w", err)
		}

		// 提取 metadata
		metadata, ok := rawData["metadata"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("missing or invalid metadata")
		}

		// 提取 spec
		spec, ok := rawData["spec"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("missing or invalid spec")
		}

		// 构建 ResourceDefinition
		def := &ResourceDefinition{
			Namespace: namespace,
			Name:      name,
			Fields:    make([]FieldDefinition, 0),
		}

		// 提取 labels
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			def.Labels = make(map[string]string)
			for k, v := range labels {
				if strVal, ok := v.(string); ok {
					def.Labels[k] = strVal
				}
			}
		}

		// 提取 fields
		if fields, ok := spec["fields"].([]interface{}); ok {
			for _, fieldRaw := range fields {
				if fieldMap, ok := fieldRaw.(map[string]interface{}); ok {
					field := FieldDefinition{}
					if ns, ok := fieldMap["namespace"].(string); ok {
						field.Namespace = ns
					}
					if name, ok := fieldMap["name"].(string); ok {
						field.Name = name
					}
					if required, ok := fieldMap["required"].(bool); ok {
						field.Required = required
					}
					def.Fields = append(def.Fields, field)
				}
			}
		}

		rsp.mu.Lock()
		if _, ok := rsp.resourceDefinitions[normalizedNs]; !ok {
			rsp.resourceDefinitions[normalizedNs] = make(map[string]*ResourceDefinition)
		}
		rsp.resourceDefinitions[normalizedNs][name] = def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded resource definition: ns=%s, name=%s", normalizedNs, name)

	case KindRelationDefinition:
		// 解析 metadata 和 spec 结构
		var rawData map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &rawData); err != nil {
			return fmt.Errorf("failed to unmarshal raw data: %w", err)
		}

		// 提取 metadata
		metadata, ok := rawData["metadata"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("missing or invalid metadata")
		}

		// 提取 spec
		spec, ok := rawData["spec"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("missing or invalid spec")
		}

		// 构建 RelationDefinition
		def := &RelationDefinition{
			Namespace: namespace,
			Name:      name,
		}

		// 提取 labels
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			def.Labels = make(map[string]string)
			for k, v := range labels {
				if strVal, ok := v.(string); ok {
					def.Labels[k] = strVal
				}
			}
		}

		// 提取 spec 字段
		if fromResource, ok := spec["from_resource"].(string); ok {
			def.FromResource = fromResource
		}
		if toResource, ok := spec["to_resource"].(string); ok {
			def.ToResource = toResource
		}
		if category, ok := spec["category"].(string); ok {
			def.Category = category
		}
		if isBelongsTo, ok := spec["is_belongs_to"].(bool); ok {
			def.IsBelongsTo = isBelongsTo
		}

		// 根据关系类型使用不同的 key
		var relationKey string
		if def.IsBelongsTo {
			// 单向关系：使用 from_to_to 格式
			relationKey = buildDirectionalRelationKey(def.FromResource, def.ToResource)
		} else {
			// 双向关系：按字母序排序
			relationKey = buildRelationKey(def.FromResource, def.ToResource)
		}

		rsp.mu.Lock()
		if _, ok := rsp.relationDefinitions[normalizedNs]; !ok {
			rsp.relationDefinitions[normalizedNs] = make(map[string]*RelationDefinition)
		}
		rsp.relationDefinitions[normalizedNs][relationKey] = def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded relation definition: ns=%s, key=%s", normalizedNs, relationKey)
	}

	return nil
}

type MsgPayload struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // KindResourceDefinition or KindRelationDefinition
}

func (m *MsgPayload) IsEmpty() bool {
	return m.Namespace == "" && m.Name == ""
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

	// 指数退避参数
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

					// 收到消息，重置退避时间
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

					logger.Infof("[schema_provider] received update: kind=%s namespace=%s name=%s", payload.Kind, payload.Namespace, payload.Name)
					if err := rsp.reloadEntity(payload.Kind, payload.Namespace, payload.Name); err != nil {
						logger.Errorf("[schema_provider] failed to reload entity: %v", err)
					}
				}
			}
		}()

		if normalExit {
			return
		}

		// 指数退避重连
		logger.Infof("[schema_provider] waiting %v before reconnecting...", backoff)
		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(backoff):
		}

		// 指数增长，最大 30 秒
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// reloadEntity 重新加载单个实体
// Redis 结构: redisKey -> namespace -> {name: jsonData, ...}
func (rsp *RedisSchemaProvider) reloadEntity(kind, namespace, name string) error {
	schemaKey := fmt.Sprintf("%s:%s", RedisKeyPrefix, kind)

	// HGET 获取 namespace 下的所有实体
	entitiesJson, err := rsp.client.HGet(rsp.ctx, schemaKey, namespace).Result()
	if errors.Is(err, redis.Nil) {
		// namespace 不存在，删除缓存中的实体
		rsp.deleteEntityFromCache(kind, namespace, name)
		logger.Infof("[schema_provider] deleted %s %s:%s (namespace not found)", kind, namespace, name)
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

	// 查找对应的实体
	jsonData, ok := entities[name]
	if !ok {
		// 实体不存在，删除缓存
		rsp.deleteEntityFromCache(kind, namespace, name)
		logger.Infof("[schema_provider] deleted %s %s:%s (entity not found)", kind, namespace, name)
		return nil
	}

	// 加载到缓存
	return rsp.loadEntityByKind(kind, namespace, name, string(jsonData))
}

// deleteEntityFromCache 从缓存删除实体
func (rsp *RedisSchemaProvider) deleteEntityFromCache(kind, namespace, name string) {
	normalizedNs := normalizeNamespace(namespace)

	rsp.mu.Lock()
	defer rsp.mu.Unlock()

	switch kind {
	case KindResourceDefinition:
		if nsMap, ok := rsp.resourceDefinitions[normalizedNs]; ok {
			delete(nsMap, name)
		}
	case KindRelationDefinition:
		if nsMap, ok := rsp.relationDefinitions[normalizedNs]; ok {
			// 需要找到对应的 relation key 来删除
			// 由于我们存储时使用了按字母序排序的 key，这里需要遍历查找
			for key, def := range nsMap {
				if def.Name == name {
					delete(nsMap, key)
					break
				}
			}
		}
	}
}

// Close 关闭 provider
func (rsp *RedisSchemaProvider) Close() error {
	rsp.cancel()
	rsp.wg.Wait()
	logger.Infof("[schema_provider] RedisSchemaProvider closed")
	return nil
}
