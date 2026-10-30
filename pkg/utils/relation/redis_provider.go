// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	// DefaultRedisPubSubChannelSuffix Pub/Sub 通道后缀
	DefaultRedisPubSubChannelSuffix = ":channel"

	// 内部默认值
	defaultRedisReconnectInterval = 5 * time.Second
	defaultRedisReconnectMaxRetry = 10
	defaultRedisReconcileInterval = time.Minute
	RelationSchemaReadyKey        = "bkmonitorv3:entity:v1beta3:schema_ready"
)

// RedisProviderConfig Redis Provider 配置
type RedisProviderConfig struct {
	ReconnectInterval time.Duration
	ReconnectMaxRetry int
	ReconcileInterval time.Duration
	ReloadOnStart     bool
	Logger            Logger // 日志接口
}

// Logger 日志接口，允许外部注入日志实现
type Logger interface {
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Debugf(format string, args ...interface{})
}

// DefaultRedisProviderConfig 返回默认配置
func DefaultRedisProviderConfig() *RedisProviderConfig {
	return &RedisProviderConfig{
		ReconnectInterval: defaultRedisReconnectInterval,
		ReconnectMaxRetry: defaultRedisReconnectMaxRetry,
		ReconcileInterval: defaultRedisReconcileInterval,
		ReloadOnStart:     true,
		Logger:            &noopLogger{},
	}
}

// noopLogger 空日志实现
type noopLogger struct{}

func (l *noopLogger) Infof(format string, args ...interface{})  {}
func (l *noopLogger) Warnf(format string, args ...interface{})  {}
func (l *noopLogger) Errorf(format string, args ...interface{}) {}
func (l *noopLogger) Debugf(format string, args ...interface{}) {}

// RedisProvider 通用 Redis Schema 提供器
// 从 Redis 动态加载资源和关联定义，支持 Pub/Sub 热更新与周期性收敛
//
// Redis 数据结构:
//   - key:   bkmonitorv3:entity:ResourceDefinition
//   - field: {namespace}
//   - value: {"name1": {JSON}, "name2": {JSON}, ...}
type RedisProvider struct {
	client redis.UniversalClient
	config *RedisProviderConfig

	// 外层 key: namespace, 内层 key: resource/relation name
	resourceDefinitions map[string]map[string]*ResourceDefinition
	relationDefinitions map[string]map[string]*RelationDefinition
	mu                  sync.RWMutex
	// Serializes full snapshots and Pub/Sub reloads so an older snapshot cannot
	// overwrite a newer namespace update.
	reloadMu sync.Mutex
	// Protected by mu; changed or invalid background snapshots invalidate it.
	ensuredVersion string

	// 订阅回调列表，在数据变更时通知
	callbacks []SchemaChangeCallback
	cbMu      sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// RedisProviderOption Redis Provider 配置选项
type RedisProviderOption func(*RedisProviderConfig)

// WithLogger 设置日志实现
func WithLogger(logger Logger) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.Logger = logger
	}
}

// WithReloadOnStart 设置启动时是否重新加载所有数据
func WithReloadOnStart(reload bool) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.ReloadOnStart = reload
	}
}

// WithReconnectConfig 设置重连配置
func WithReconnectConfig(interval time.Duration, maxRetry int) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.ReconnectInterval = interval
		config.ReconnectMaxRetry = maxRetry
	}
}

// WithReconcileInterval sets the interval for recovering missed Pub/Sub updates.
// A non-positive interval disables periodic reconciliation.
func WithReconcileInterval(interval time.Duration) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.ReconcileInterval = interval
	}
}

// NewRedisProvider 创建 Redis Provider
func NewRedisProvider(ctx context.Context, client redis.UniversalClient, opts ...RedisProviderOption) (*RedisProvider, error) {
	config := DefaultRedisProviderConfig()
	for _, opt := range opts {
		opt(config)
	}

	ctx, cancel := context.WithCancel(ctx)
	provider := &RedisProvider{
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
	if config.ReconcileInterval > 0 {
		provider.wg.Add(1)
		go provider.reconcilePeriodically()
	}

	return provider, nil
}

// Close 关闭提供器，幂等，可多次调用
func (rp *RedisProvider) Close() error {
	rp.mu.Lock()
	if rp.cancel == nil {
		rp.mu.Unlock()
		return nil
	}
	cancel := rp.cancel
	rp.cancel = nil
	rp.mu.Unlock()

	cancel()
	rp.wg.Wait()
	rp.config.Logger.Infof("RedisProvider closed")
	return nil
}

// normalizeNamespace 规范化 namespace，空的映射到 __all__
func (rp *RedisProvider) normalizeNamespace(namespace string) string {
	if namespace == "" {
		return NamespaceAll
	}
	return namespace
}

// loadAllEntities atomically replaces both kinds after reading a complete Redis
// snapshot. Missing fields must remove cached namespaces after a lost Pub/Sub
// deletion, while a read failure must leave the previous snapshot untouched.
func (rp *RedisProvider) loadAllEntities() error {
	return rp.loadAllEntitiesWithContext(rp.ctx, false)
}

func (rp *RedisProvider) loadAllEntitiesWithContext(ctx context.Context, strict bool) error {
	rp.reloadMu.Lock()
	defer rp.reloadMu.Unlock()
	return rp.loadAllEntitiesLocked(ctx, strict)
}

// Caller holds reloadMu across snapshot reads, cache replacement and callbacks.
func (rp *RedisProvider) loadAllEntitiesLocked(ctx context.Context, strict bool) error {
	resourceFields, err := rp.client.HGetAll(ctx, RedisKeyPrefix+":"+KindResourceDefinition).Result()
	if err != nil {
		return fmt.Errorf("failed to load resource definitions: %w", err)
	}
	relationFields, err := rp.client.HGetAll(ctx, RedisKeyPrefix+":"+KindRelationDefinition).Result()
	if err != nil {
		return fmt.Errorf("failed to load relation definitions: %w", err)
	}

	resources, invalidResources := rp.decodeResourceFields(resourceFields)
	relations, invalidRelations := rp.decodeRelationFields(relationFields)
	if strict {
		if len(invalidResources) > 0 || len(invalidRelations) > 0 {
			return fmt.Errorf("invalid relation schema snapshot: resources=%d relations=%d", len(invalidResources), len(invalidRelations))
		}
		if len(resources[NamespaceAll]) == 0 || len(relations[NamespaceAll]) == 0 {
			return fmt.Errorf("shared relation schema is incomplete")
		}
	}

	rp.mu.Lock()
	for namespace := range invalidResources {
		if previous, ok := rp.resourceDefinitions[namespace]; ok {
			resources[namespace] = previous
		}
	}
	for namespace := range invalidRelations {
		if previous, ok := rp.relationDefinitions[namespace]; ok {
			relations[namespace] = previous
		}
	}
	resourceChanges := changedNamespaces(rp.resourceDefinitions, resources)
	relationChanges := changedNamespaces(rp.relationDefinitions, relations)
	rp.resourceDefinitions = resources
	rp.relationDefinitions = relations
	if len(resourceChanges)+len(relationChanges)+len(invalidResources)+len(invalidRelations) > 0 {
		rp.ensuredVersion = ""
	}
	rp.mu.Unlock()

	for _, namespace := range resourceChanges {
		rp.triggerCallbacks(KindResourceDefinition, namespace)
	}
	for _, namespace := range relationChanges {
		rp.triggerCallbacks(KindRelationDefinition, namespace)
	}
	if changed := len(resourceChanges) + len(relationChanges); changed > 0 {
		rp.config.Logger.Infof("reconciled Redis definitions: resources=%d relations=%d changed_namespaces=%d",
			len(resources), len(relations), changed)
	}
	return nil
}

// EnsureVersion guarantees that this process has loaded the schema snapshot
// published before the requested rollout version became ready.
func (rp *RedisProvider) EnsureVersion(ctx context.Context, version string) error {
	if version == "" {
		return fmt.Errorf("relation schema version is empty")
	}

	readVersion := func() (string, error) {
		payload, err := rp.client.Get(ctx, RelationSchemaReadyKey).Bytes()
		if err != nil {
			return "", fmt.Errorf("read relation schema readiness: %w", err)
		}
		var ready struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(payload, &ready); err != nil {
			return "", fmt.Errorf("decode relation schema readiness: %w", err)
		}
		return ready.Version, nil
	}

	currentVersion, err := readVersion()
	if err != nil {
		return err
	}
	if currentVersion != version {
		return fmt.Errorf("relation schema version changed before query: requested=%s current=%s", version, currentVersion)
	}
	rp.mu.RLock()
	loaded := rp.ensuredVersion == version
	rp.mu.RUnlock()
	if loaded {
		return nil
	}

	// Serialize cold loads only. Healthy requests perform their Redis readiness
	// check concurrently instead of queuing behind every other request's I/O.
	rp.reloadMu.Lock()
	defer rp.reloadMu.Unlock()
	rp.mu.RLock()
	loaded = rp.ensuredVersion == version
	rp.mu.RUnlock()
	if loaded {
		return nil
	}
	if err := rp.loadAllEntitiesLocked(ctx, true); err != nil {
		return err
	}
	currentVersion, err = readVersion()
	if err != nil {
		return err
	}
	if currentVersion != version {
		return fmt.Errorf("relation schema version changed while loading: requested=%s current=%s", version, currentVersion)
	}
	rp.mu.Lock()
	rp.ensuredVersion = version
	rp.mu.Unlock()
	return nil
}

func (rp *RedisProvider) decodeResourceFields(fields map[string]string) (map[string]map[string]*ResourceDefinition, map[string]struct{}) {
	definitions := make(map[string]map[string]*ResourceDefinition, len(fields))
	invalid := make(map[string]struct{})
	for field, value := range fields {
		namespace := rp.normalizeNamespace(field)
		var entries map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &entries); err != nil {
			rp.config.Logger.Warnf("invalid resource definitions for namespace %s: %v", namespace, err)
			invalid[namespace] = struct{}{}
			continue
		}
		parsed := make(map[string]*ResourceDefinition, len(entries))
		for name, data := range entries {
			var definition ResourceDefinition
			if err := json.Unmarshal(data, &definition); err != nil {
				rp.config.Logger.Warnf("invalid resource definition %s:%s: %v", namespace, name, err)
				invalid[namespace] = struct{}{}
				break
			}
			parsed[name] = &definition
		}
		if _, bad := invalid[namespace]; bad {
			continue
		}
		definitions[namespace] = parsed
	}
	return definitions, invalid
}

func (rp *RedisProvider) decodeRelationFields(fields map[string]string) (map[string]map[string]*RelationDefinition, map[string]struct{}) {
	definitions := make(map[string]map[string]*RelationDefinition, len(fields))
	invalid := make(map[string]struct{})
	for field, value := range fields {
		namespace := rp.normalizeNamespace(field)
		var entries map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &entries); err != nil {
			rp.config.Logger.Warnf("invalid relation definitions for namespace %s: %v", namespace, err)
			invalid[namespace] = struct{}{}
			continue
		}
		parsed := make(map[string]*RelationDefinition, len(entries))
		for name, data := range entries {
			var definition RelationDefinition
			if err := json.Unmarshal(data, &definition); err != nil {
				rp.config.Logger.Warnf("invalid relation definition %s:%s: %v", namespace, name, err)
				invalid[namespace] = struct{}{}
				break
			}
			parsed[name] = &definition
		}
		if _, bad := invalid[namespace]; bad {
			continue
		}
		definitions[namespace] = parsed
	}
	return definitions, invalid
}

func changedNamespaces[T any](previous, current map[string]map[string]*T) []string {
	namespaces := make(map[string]struct{}, len(previous)+len(current))
	for namespace := range previous {
		namespaces[namespace] = struct{}{}
	}
	for namespace := range current {
		namespaces[namespace] = struct{}{}
	}
	changed := make([]string, 0)
	for namespace := range namespaces {
		if !reflect.DeepEqual(previous[namespace], current[namespace]) {
			changed = append(changed, namespace)
		}
	}
	sort.Strings(changed)
	return changed
}

func (rp *RedisProvider) reconcilePeriodically() {
	defer rp.wg.Done()
	ticker := time.NewTicker(rp.config.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rp.ctx.Done():
			return
		case <-ticker.C:
			if err := rp.loadAllEntities(); err != nil && rp.ctx.Err() == nil {
				rp.config.Logger.Errorf("periodic Redis schema reconciliation failed: %v", err)
			}
		}
	}
}

// reloadNamespace 按 namespace 全量重建该 kind 的本地缓存
func (rp *RedisProvider) reloadNamespace(kind, namespace string) error {
	rp.reloadMu.Lock()
	defer rp.reloadMu.Unlock()

	// 收到 schema 通知后，即使读取或解码失败、旧查询继续使用原快照，
	// 就绪检查仍需重新验证，不能沿用此前验证通过的版本。
	if kind == KindResourceDefinition || kind == KindRelationDefinition {
		rp.mu.Lock()
		rp.ensuredVersion = ""
		rp.mu.Unlock()
	}

	redisKey := RedisKeyPrefix + ":" + kind
	normalizedNs := rp.normalizeNamespace(namespace)

	entitiesJSON, err := rp.client.HGet(rp.ctx, redisKey, namespace).Result()
	if errors.Is(err, redis.Nil) {
		// namespace 已不存在，清空缓存
		rp.mu.Lock()
		rp.ensuredVersion = ""
		switch kind {
		case KindResourceDefinition:
			delete(rp.resourceDefinitions, normalizedNs)
		case KindRelationDefinition:
			delete(rp.relationDefinitions, normalizedNs)
		}
		rp.mu.Unlock()
		rp.config.Logger.Infof("cleared namespace cache: kind=%s, ns=%s", kind, namespace)
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
	case KindResourceDefinition:
		newMap := make(map[string]*ResourceDefinition, len(entities))
		for name, jsonData := range entities {
			var def ResourceDefinition
			if unmarshalErr := json.Unmarshal(jsonData, &def); unmarshalErr != nil {
				rp.config.Logger.Warnf("failed to unmarshal ResourceDefinition %s:%s: %v", namespace, name, unmarshalErr)
				continue
			}
			newMap[name] = &def
		}
		rp.mu.Lock()
		rp.resourceDefinitions[normalizedNs] = newMap
		rp.mu.Unlock()

	case KindRelationDefinition:
		newMap := make(map[string]*RelationDefinition, len(entities))
		for name, jsonData := range entities {
			var def RelationDefinition
			if unmarshalErr := json.Unmarshal(jsonData, &def); unmarshalErr != nil {
				rp.config.Logger.Warnf("failed to unmarshal RelationDefinition %s:%s: %v", namespace, name, unmarshalErr)
				continue
			}
			newMap[name] = &def
		}
		rp.mu.Lock()
		rp.relationDefinitions[normalizedNs] = newMap
		rp.mu.Unlock()
	}

	rp.config.Logger.Infof("reloaded namespace cache: kind=%s, ns=%s, count=%d", kind, namespace, len(entities))
	return nil
}

// MsgPayload Pub/Sub 消息体
type MsgPayload struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
}

// subscribeEntities 订阅实体变更通知
func (rp *RedisProvider) subscribeEntities() {
	defer rp.wg.Done()

	channels := []string{
		fmt.Sprintf("%s:%s%s", RedisKeyPrefix, KindResourceDefinition, DefaultRedisPubSubChannelSuffix),
		fmt.Sprintf("%s:%s%s", RedisKeyPrefix, KindRelationDefinition, DefaultRedisPubSubChannelSuffix),
	}

	retryCount := 0
	for {
		select {
		case <-rp.ctx.Done():
			return
		default:
		}

		pubsub := rp.client.Subscribe(rp.ctx, channels...)
		rp.config.Logger.Infof("subscribed to entity channels: %v", channels)

		done := make(chan struct{})
		go func() {
			defer close(done)
			ch := pubsub.Channel()
			// Subscribe first, then reconcile changes missed while disconnected.
			if err := rp.loadAllEntities(); err != nil && rp.ctx.Err() == nil {
				rp.config.Logger.Errorf("Redis schema reconciliation after subscription failed: %v", err)
			}
			for {
				select {
				case <-rp.ctx.Done():
					return
				case msg, ok := <-ch:
					if !ok {
						return
					}
					if msg == nil {
						continue
					}

					var payload MsgPayload
					if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
						rp.config.Logger.Warnf("invalid pubsub payload: %s, err: %v", msg.Payload, err)
						continue
					}
					if payload.Namespace == "" || payload.Kind == "" {
						rp.config.Logger.Warnf("empty namespace or kind in payload: %s", msg.Payload)
						continue
					}

					rp.config.Logger.Infof("received entity update: kind=%s namespace=%s", payload.Kind, payload.Namespace)
					if err := rp.reloadNamespace(payload.Kind, payload.Namespace); err != nil {
						rp.config.Logger.Errorf("failed to reload namespace %s:%s: %v", payload.Kind, payload.Namespace, err)
					}

					rp.triggerCallbacks(payload.Kind, payload.Namespace)
				}
			}
		}()

		select {
		case <-rp.ctx.Done():
			pubsub.Close()
			<-done
			return
		case <-done:
			pubsub.Close()
		}

		retryCount++
		if retryCount > rp.config.ReconnectMaxRetry {
			rp.config.Logger.Errorf("max retry count reached for entity subscription")
			return
		}

		rp.config.Logger.Warnf("entity subscription disconnected, retrying in %v (attempt %d/%d)",
			rp.config.ReconnectInterval, retryCount, rp.config.ReconnectMaxRetry)

		select {
		case <-rp.ctx.Done():
			return
		case <-time.After(rp.config.ReconnectInterval):
		}
	}
}

// GetResourceDefinition 获取资源定义
func (rp *RedisProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	ns := rp.normalizeNamespace(namespace)

	// 先从指定 namespace 查找
	if nsMap, ok := rp.resourceDefinitions[ns]; ok {
		if def, ok := nsMap[name]; ok {
			return def, nil
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if ns != NamespaceAll {
		if allMap, ok := rp.resourceDefinitions[NamespaceAll]; ok {
			if def, ok := allMap[name]; ok {
				return def, nil
			}
		}
	}

	rp.config.Logger.Debugf("resource definition not found: namespace=%s, name=%s", namespace, name)
	return nil, ErrResourceDefinitionNotFound
}

// ListResourceDefinitions 列出指定命名空间下的所有资源定义
// namespace 为空或 "__all__" 时只返回全局定义；指定业务 namespace 时合并 __all__ 作为兜底
func (rp *RedisProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make([]*ResourceDefinition, 0)
	ns := rp.normalizeNamespace(namespace)
	seen := make(map[string]struct{})

	if nsMap, ok := rp.resourceDefinitions[ns]; ok {
		for name, def := range nsMap {
			result = append(result, def)
			seen[name] = struct{}{}
		}
	}

	// 合并 __all__ 的定义（指定 namespace 优先）
	if ns != NamespaceAll {
		if allMap, ok := rp.resourceDefinitions[NamespaceAll]; ok {
			for name, def := range allMap {
				if _, exists := seen[name]; !exists {
					result = append(result, def)
				}
			}
		}
	}
	return result, nil
}

// ListAllResourceDefinitions 返回所有命名空间下的资源定义，按 namespace 分组
func (rp *RedisProvider) ListAllResourceDefinitions() (map[string][]*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make(map[string][]*ResourceDefinition, len(rp.resourceDefinitions))
	for ns, nsMap := range rp.resourceDefinitions {
		defs := make([]*ResourceDefinition, 0, len(nsMap))
		for _, def := range nsMap {
			defs = append(defs, def)
		}
		result[ns] = defs
	}
	return result, nil
}

// GetRelationDefinition 获取关联定义
func (rp *RedisProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	ns := rp.normalizeNamespace(namespace)

	// 先从指定 namespace 查找
	if nsMap, ok := rp.relationDefinitions[ns]; ok {
		if def, ok := nsMap[name]; ok {
			return def, nil
		}
	}

	// 如果指定 namespace 没找到，尝试从 __all__ 查找
	if ns != NamespaceAll {
		if allMap, ok := rp.relationDefinitions[NamespaceAll]; ok {
			if def, ok := allMap[name]; ok {
				return def, nil
			}
		}
	}

	rp.config.Logger.Debugf("relation definition not found: namespace=%s, name=%s", namespace, name)
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationDefinitions 列出指定命名空间下的所有关联定义
// namespace 为空或 "__all__" 时只返回全局定义；指定业务 namespace 时合并 __all__ 作为兜底
func (rp *RedisProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make([]*RelationDefinition, 0)
	ns := rp.normalizeNamespace(namespace)
	seen := make(map[string]struct{})

	if nsMap, ok := rp.relationDefinitions[ns]; ok {
		for name, def := range nsMap {
			result = append(result, def)
			seen[name] = struct{}{}
		}
	}

	// 合并 __all__（指定 namespace 优先）
	if ns != NamespaceAll {
		if allMap, ok := rp.relationDefinitions[NamespaceAll]; ok {
			for name, def := range allMap {
				if _, exists := seen[name]; !exists {
					result = append(result, def)
				}
			}
		}
	}
	return result, nil
}

// ListAllRelationDefinitions 返回所有命名空间下的关联定义，按 namespace 分组
func (rp *RedisProvider) ListAllRelationDefinitions() (map[string][]*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make(map[string][]*RelationDefinition, len(rp.relationDefinitions))
	for ns, nsMap := range rp.relationDefinitions {
		defs := make([]*RelationDefinition, 0, len(nsMap))
		for _, def := range nsMap {
			defs = append(defs, def)
		}
		result[ns] = defs
	}
	return result, nil
}

// GetResourcePrimaryKeys 获取资源类型的主键字段列表
func (rp *RedisProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, nsMap := range rp.resourceDefinitions {
		for _, rd := range nsMap {
			if ToResourceType(rd) == resourceType {
				return rd.GetPrimaryKeys()
			}
		}
	}
	return []string{}
}

// GetRelationSchema 获取关联关系的 Schema
func (rp *RedisProvider) GetRelationSchema(relationType RelationName) (*RelationSchema, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, nsMap := range rp.relationDefinitions {
		for _, rd := range nsMap {
			if ToRelationName(rd) == relationType {
				schema := ToRelationSchema(rd)
				return &schema, nil
			}
		}
	}
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出所有关联 Schema
func (rp *RedisProvider) ListRelationSchemas() []RelationSchema {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make([]RelationSchema, 0)
	for _, nsMap := range rp.relationDefinitions {
		for _, rd := range nsMap {
			result = append(result, ToRelationSchema(rd))
		}
	}
	return result
}

// FindRelationByResourceTypes 根据资源类型和方向类型查找关联定义
// 这是为了支持 bmw 的查询方式
func (rp *RedisProvider) FindRelationByResourceTypes(namespace, fromResource, toResource string, directionType DirectionType) (*RelationDefinition, bool) {
	defs, err := rp.ListRelationDefinitions(namespace)
	if err != nil {
		rp.config.Logger.Warnf("FindRelationByResourceTypes: list relation definitions failed, namespace=%s, err=%v", namespace, err)
		return nil, false
	}

	for _, def := range defs {
		switch directionType {
		case DirectionTypeDirectional:
			// 单向关联：必须严格匹配 from -> to
			if def.IsDirectional && def.FromResource == fromResource && def.ToResource == toResource {
				return def, true
			}
		case DirectionTypeBidirectional:
			// 双向关联：匹配任意方向
			if !def.IsDirectional &&
				((def.FromResource == fromResource && def.ToResource == toResource) ||
					(def.FromResource == toResource && def.ToResource == fromResource)) {
				return def, true
			}
		}
	}

	return nil, false
}

// Subscribe registers a callback to be invoked when schema changes occur
// The callback will be called with the kind ("ResourceDefinition" or "RelationDefinition")
// and namespace that was reloaded
func (rp *RedisProvider) Name() string {
	return "redis"
}

// ListNamespaces returns all namespaces that have resource definitions
func (rp *RedisProvider) ListNamespaces() ([]string, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	namespaces := make([]string, 0, len(rp.resourceDefinitions))
	for ns := range rp.resourceDefinitions {
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

func (rp *RedisProvider) Subscribe(callback SchemaChangeCallback) error {
	if callback == nil {
		return errors.New("callback cannot be nil")
	}

	rp.cbMu.Lock()
	rp.callbacks = append(rp.callbacks, callback)
	rp.cbMu.Unlock()

	rp.config.Logger.Debugf("schema change callback registered")
	return nil
}

// triggerCallbacks invokes all registered callbacks
func (rp *RedisProvider) triggerCallbacks(kind, namespace string) {
	rp.cbMu.RLock()
	callbacks := make([]SchemaChangeCallback, len(rp.callbacks))
	copy(callbacks, rp.callbacks)
	rp.cbMu.RUnlock()

	for _, callback := range callbacks {
		// Run callbacks in separate goroutines to avoid blocking the subscription loop
		go func(cb SchemaChangeCallback) {
			defer func() {
				if r := recover(); r != nil {
					rp.config.Logger.Errorf("panic in schema change callback: %v", r)
				}
			}()
			cb(kind, namespace)
		}(callback)
	}
}

// Ensure RedisProvider implements SchemaProvider
var _ SchemaProvider = (*RedisProvider)(nil)
