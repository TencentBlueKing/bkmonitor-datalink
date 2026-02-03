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
	"fmt"
	"strings"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// RedisKeyPrefix Redis Key 前缀
	RedisKeyPrefix = "bkmonitorv3:entity"

	// 实体类型
	KindResourceDefinition = "ResourceDefinition"
	KindRelationDefinition = "RelationDefinition"
)

// RedisSchemaProvider Redis 实现的 SchemaProvider
type RedisSchemaProvider struct {
	client redis.UniversalClient

	// 本地缓存
	resourceDefinitions map[string]*ResourceDefinition // key: namespace:name
	relationDefinitions map[string]*RelationDefinition // key: namespace:name

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRedisSchemaProvider 创建 RedisSchemaProvider
func NewRedisSchemaProvider(client redis.UniversalClient) (*RedisSchemaProvider, error) {
	ctx, cancel := context.WithCancel(context.Background())

	provider := &RedisSchemaProvider{
		client:              client,
		resourceDefinitions: make(map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]*RelationDefinition),
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

// GetResourceDefinition 获取资源定义
func (rsp *RedisSchemaProvider) GetResourceDefinition(namespace, resourceType string) (*ResourceDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", namespace, resourceType)
	if def, ok := rsp.resourceDefinitions[key]; ok {
		return def, nil
	}

	return nil, fmt.Errorf("resource definition not found: %s", key)
}

// GetRelationDefinition 获取关系定义
func (rsp *RedisSchemaProvider) GetRelationDefinition(namespace, fromResource, toResource string) (*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	// 尝试查找 from_with_to
	name := fmt.Sprintf("%s_with_%s", fromResource, toResource)
	key := fmt.Sprintf("%s:%s", namespace, name)

	if def, ok := rsp.relationDefinitions[key]; ok {
		return def, nil
	}

	return nil, fmt.Errorf("relation definition not found: %s", key)
}

// ListRelationDefinitions 列出所有关系定义
func (rsp *RedisSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	definitions := make([]*RelationDefinition, 0)
	prefix := namespace + ":"

	for key, def := range rsp.relationDefinitions {
		if strings.HasPrefix(key, prefix) {
			definitions = append(definitions, def)
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

	logger.Infof("[schema_provider] loaded %d resource definitions, %d relation definitions",
		len(rsp.resourceDefinitions), len(rsp.relationDefinitions))

	return nil
}

// loadEntitiesByKind 按 kind 加载实体
func (rsp *RedisSchemaProvider) loadEntitiesByKind(kind string) error {
	ctx := context.Background()
	redisKey := fmt.Sprintf("%s:%s", RedisKeyPrefix, kind)

	// HGETALL 获取所有数据
	result, err := rsp.client.HGetAll(ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("failed to hgetall %s: %w", redisKey, err)
	}

	// 解析并存储
	count := 0
	for field, jsonData := range result {
		if err := rsp.loadEntityByKind(kind, field, jsonData); err != nil {
			logger.Warnf("[schema_provider] failed to load %s %s: %v", kind, field, err)
			continue
		}
		count++
	}

	logger.Debugf("[schema_provider] loaded %d entities of kind %s", count, kind)

	return nil
}

// loadEntityByKind 按 kind 加载单个实体
func (rsp *RedisSchemaProvider) loadEntityByKind(kind, field, jsonData string) error {
	// 解析 namespace:name
	parts := strings.SplitN(field, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid field format: %s", field)
	}

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
			Namespace: parts[0],
			Name:      parts[1],
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
		rsp.resourceDefinitions[field] = def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded resource definition: %s", field)

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
			Namespace: parts[0],
			Name:      parts[1],
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

		rsp.mu.Lock()
		rsp.relationDefinitions[field] = def
		rsp.mu.Unlock()

		logger.Debugf("[schema_provider] loaded relation definition: %s", field)
	}

	return nil
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

	for {
		select {
		case <-rsp.ctx.Done():
			logger.Infof("[schema_provider] subscription stopped")
			return
		default:
		}

		pubsub := rsp.client.Subscribe(rsp.ctx, channels...)

		func() {
			defer pubsub.Close()

			ch := pubsub.Channel()
			for {
				select {
				case <-rsp.ctx.Done():
					return
				case msg, ok := <-ch:
					if !ok {
						logger.Warnf("[schema_provider] pubsub channel closed, reconnecting...")
						return
					}

					if msg == nil {
						continue
					}

					// 提取 kind
					kind := extractKindFromChannel(msg.Channel)
					if kind == "" {
						logger.Warnf("[schema_provider] invalid channel: %s", msg.Channel)
						continue
					}

					// msg.Payload: "namespace:name"
					field := msg.Payload

					logger.Infof("[schema_provider] received update: kind=%s field=%s", kind, field)

					// 从 Redis 重新加载
					if err := rsp.reloadEntity(kind, field); err != nil {
						logger.Errorf("[schema_provider] failed to reload entity: %v", err)
					}
				}
			}
		}()
	}
}

// reloadEntity 重新加载单个实体
func (rsp *RedisSchemaProvider) reloadEntity(kind, field string) error {
	ctx := context.Background()
	redisKey := fmt.Sprintf("%s:%s", RedisKeyPrefix, kind)

	// HGET 获取数据
	jsonData, err := rsp.client.HGet(ctx, redisKey, field).Result()
	if err == redis.Nil {
		// 数据已删除
		rsp.deleteEntityFromCache(kind, field)
		logger.Infof("[schema_provider] deleted %s %s", kind, field)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to hget: %w", err)
	}

	// 加载到缓存
	return rsp.loadEntityByKind(kind, field, jsonData)
}

// deleteEntityFromCache 从缓存删除实体
func (rsp *RedisSchemaProvider) deleteEntityFromCache(kind, field string) {
	rsp.mu.Lock()
	defer rsp.mu.Unlock()

	switch kind {
	case KindResourceDefinition:
		delete(rsp.resourceDefinitions, field)
	case KindRelationDefinition:
		delete(rsp.relationDefinitions, field)
	}
}

// extractKindFromChannel 从 channel 提取 kind
// 输入: "bkmonitorv3:entity:ResourceDefinition:channel"
// 输出: "ResourceDefinition"
func extractKindFromChannel(channel string) string {
	parts := strings.Split(channel, ":")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// Close 关闭 provider
func (rsp *RedisSchemaProvider) Close() error {
	rsp.cancel()
	rsp.wg.Wait()
	logger.Infof("[schema_provider] RedisSchemaProvider closed")
	return nil
}
