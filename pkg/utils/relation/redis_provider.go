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
)

// RedisProviderConfig Redis Provider 配置
type RedisProviderConfig struct {
	ReconnectInterval time.Duration
	ReconnectMaxRetry int
	ReconcileInterval time.Duration
	ReloadOnStart     bool
	Logger            Logger // 日志接口
	// BaseSchema 是 __all__ 的底层（内置）定义；Redis 里的定义只作覆盖层，
	// 同名定义整条覆盖内置，删掉后内置恢复生效。
	BaseSchema StaticProviderConfig
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
		BaseSchema:        DefaultStaticProviderConfig(),
	}
}

// noopLogger 空日志实现
type noopLogger struct{}

func (l *noopLogger) Infof(format string, args ...interface{})  {}
func (l *noopLogger) Warnf(format string, args ...interface{})  {}
func (l *noopLogger) Errorf(format string, args ...interface{}) {}
func (l *noopLogger) Debugf(format string, args ...interface{}) {}

// RedisProvider 通用 Redis Schema 提供器
// 以内置定义作 __all__ 底层，从 Redis 动态加载覆盖层，支持 Pub/Sub 热更新与周期性收敛。
// 查找顺序：业务 namespace（Redis）→ __all__（Redis）→ 内置；Redis 为空时等于完整内置。
//
// Redis 数据结构:
//   - key:   bkmonitorv3:entity:ResourceDefinition
//   - field: {namespace}
//   - value: {"name1": {JSON}, "name2": {JSON}, ...}
type RedisProvider struct {
	client redis.UniversalClient
	config *RedisProviderConfig

	// Redis 覆盖层。外层 key: namespace, 内层 key: resource/relation name
	resourceDefinitions map[string]map[string]*ResourceDefinition
	relationDefinitions map[string]map[string]*RelationDefinition
	// 内置底层，构造后不再变化；订阅与对账只更新覆盖层。
	builtinResources map[string]*ResourceDefinition
	builtinRelations map[string]*RelationDefinition
	// 覆盖层读取状态，供诊断接口与指标使用。
	statusMu sync.Mutex
	status   overlayStatus
	mu       sync.RWMutex
	// Serializes full snapshots and Pub/Sub reloads so an older snapshot cannot
	// overwrite a newer namespace update.
	reloadMu sync.Mutex

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

// WithBaseSchema 设置内置底层定义，默认 DefaultStaticProviderConfig。
func WithBaseSchema(base StaticProviderConfig) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.BaseSchema = base
	}
}

// WithReconcileInterval sets the interval for recovering missed Pub/Sub updates.
// A non-positive interval disables periodic reconciliation.
func WithReconcileInterval(interval time.Duration) RedisProviderOption {
	return func(config *RedisProviderConfig) {
		config.ReconcileInterval = interval
	}
}

// NewRedisProvider 创建 Redis Provider，不会失败：内置定义立即可用。
// client 为 nil 时覆盖层不可用，只有内置一层；首次读取 Redis 失败时先用内置定义，
// 订阅建立后与每次周期对账都会重试，成功后覆盖层生效。
func NewRedisProvider(ctx context.Context, client redis.UniversalClient, opts ...RedisProviderOption) *RedisProvider {
	config := DefaultRedisProviderConfig()
	for _, opt := range opts {
		opt(config)
	}

	provider := newRedisProvider(ctx, client, config)
	if client == nil {
		provider.recordFailure(OverlayReasonClientMissing, errors.New("redis client is not configured"))
		config.Logger.Warnf("Redis client is not configured, serving builtin relation definitions only")
		return provider
	}
	if config.ReloadOnStart {
		if err := provider.loadAllEntities(); err != nil {
			config.Logger.Errorf("initial Redis schema load failed, serving builtin definitions until a reload succeeds: %v", err)
		}
	}

	provider.wg.Add(1)
	go provider.subscribeEntities()
	if config.ReconcileInterval > 0 {
		provider.wg.Add(1)
		go provider.reconcilePeriodically()
	}

	return provider
}

// newRedisProvider 构造覆盖层为空、内置底层就绪的 Provider，不读取 Redis，也不启动订阅。
func newRedisProvider(ctx context.Context, client redis.UniversalClient, config *RedisProviderConfig) *RedisProvider {
	ctx, cancel := context.WithCancel(ctx)
	base := NewStaticSchemaProvider(config.BaseSchema)
	return &RedisProvider{
		client:              client,
		config:              config,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
		builtinResources:    base.resourceDefinitions,
		builtinRelations:    base.relationDefinitions,
		ctx:                 ctx,
		cancel:              cancel,
	}
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
	rp.reloadMu.Lock()
	defer rp.reloadMu.Unlock()

	resourceFields, err := rp.client.HGetAll(rp.ctx, RedisKeyPrefix+":"+KindResourceDefinition).Result()
	if err != nil {
		err = fmt.Errorf("failed to load resource definitions: %w", err)
		rp.recordFailure(OverlayReasonReadFailed, err)
		return err
	}
	relationFields, err := rp.client.HGetAll(rp.ctx, RedisKeyPrefix+":"+KindRelationDefinition).Result()
	if err != nil {
		err = fmt.Errorf("failed to load relation definitions: %w", err)
		rp.recordFailure(OverlayReasonReadFailed, err)
		return err
	}

	resources, invalidResources := rp.decodeResourceFields(resourceFields)
	relations, invalidRelations := rp.decodeRelationFields(relationFields)
	for namespace := range invalidResources {
		rp.recordFailure(OverlayReasonDecodeFailed, fmt.Errorf("invalid resource definitions in namespace %s", namespace))
	}
	for namespace := range invalidRelations {
		rp.recordFailure(OverlayReasonDecodeFailed, fmt.Errorf("invalid relation definitions in namespace %s", namespace))
	}
	rp.recordSuccess()

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
	inheritors := rp.businessNamespacesLocked()
	rp.resourceDefinitions = resources
	rp.relationDefinitions = relations
	inheritors = mergeNamespaces(inheritors, rp.businessNamespacesLocked())
	rp.mu.Unlock()

	for _, namespace := range withInheritors(resourceChanges, inheritors) {
		rp.triggerCallbacks(KindResourceDefinition, namespace)
	}
	for _, namespace := range withInheritors(relationChanges, inheritors) {
		rp.triggerCallbacks(KindRelationDefinition, namespace)
	}
	if changed := len(resourceChanges) + len(relationChanges); changed > 0 {
		rp.config.Logger.Infof("reconciled Redis definitions: resources=%d relations=%d changed_namespaces=%d",
			len(resources), len(relations), changed)
	}
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

// businessNamespacesLocked 返回覆盖层里除 __all__ 外的 namespace，调用方需持有 rp.mu。
func (rp *RedisProvider) businessNamespacesLocked() []string {
	namespaces := make([]string, 0, len(rp.resourceDefinitions)+len(rp.relationDefinitions))
	for namespace := range rp.resourceDefinitions {
		namespaces = append(namespaces, namespace)
	}
	for namespace := range rp.relationDefinitions {
		namespaces = append(namespaces, namespace)
	}
	return mergeNamespaces(namespaces)
}

// mergeNamespaces 合并、去重并排序，去掉 __all__。
func mergeNamespaces(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, namespace := range group {
			if namespace != NamespaceAll {
				seen[namespace] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for namespace := range seen {
		result = append(result, namespace)
	}
	sort.Strings(result)
	return result
}

// withInheritors 在 __all__ 变化时补上各业务 namespace：它们继承 __all__，生效定义也跟着变。
func withInheritors(changed, inheritors []string) []string {
	for _, namespace := range changed {
		if namespace == NamespaceAll {
			return append([]string{NamespaceAll}, mergeNamespaces(changed, inheritors)...)
		}
	}
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

	redisKey := RedisKeyPrefix + ":" + kind
	normalizedNs := rp.normalizeNamespace(namespace)

	entitiesJSON, err := rp.client.HGet(rp.ctx, redisKey, namespace).Result()
	if errors.Is(err, redis.Nil) {
		// namespace 已不存在，清空缓存
		rp.mu.Lock()
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
		err = fmt.Errorf("failed to hget: %w", err)
		rp.recordFailure(OverlayReasonReadFailed, err)
		return err
	}

	var entities map[string]json.RawMessage
	if err = json.Unmarshal([]byte(entitiesJSON), &entities); err != nil {
		err = fmt.Errorf("failed to unmarshal entities: %w", err)
		rp.recordFailure(OverlayReasonDecodeFailed, err)
		return err
	}

	switch kind {
	case KindResourceDefinition:
		newMap := make(map[string]*ResourceDefinition, len(entities))
		for name, jsonData := range entities {
			var def ResourceDefinition
			if unmarshalErr := json.Unmarshal(jsonData, &def); unmarshalErr != nil {
				rp.config.Logger.Warnf("failed to unmarshal ResourceDefinition %s:%s: %v", namespace, name, unmarshalErr)
				rp.recordFailure(OverlayReasonDecodeFailed, unmarshalErr)
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
				rp.recordFailure(OverlayReasonDecodeFailed, unmarshalErr)
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
			// Reconcile after establishing this subscription. go-redis also reconnects
			// internally without closing ch; periodic reconciliation covers those gaps.
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
					if rp.normalizeNamespace(payload.Namespace) == NamespaceAll {
						rp.mu.RLock()
						inheritors := rp.businessNamespacesLocked()
						rp.mu.RUnlock()
						for _, namespace := range inheritors {
							rp.triggerCallbacks(payload.Kind, namespace)
						}
					}
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

// GetResourceDefinition 获取资源定义，按 业务 namespace → __all__ → 内置 查找
func (rp *RedisProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	if def, ok := lookupLayered(rp.resourceDefinitions, rp.builtinResources, rp.normalizeNamespace(namespace), name); ok {
		return def, nil
	}
	rp.config.Logger.Debugf("resource definition not found: namespace=%s, name=%s", namespace, name)
	return nil, ErrResourceDefinitionNotFound
}

// ListResourceDefinitions 列出指定命名空间下生效的资源定义：
// 业务 namespace、__all__ 与内置按查找顺序合并，同名只取上层整条定义
func (rp *RedisProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return definitionsOf(listLayered(rp.resourceDefinitions, rp.builtinResources, rp.normalizeNamespace(namespace))), nil
}

// ListAllResourceDefinitions 返回 __all__ 与覆盖层各 namespace 生效的资源定义，按 namespace 分组
func (rp *RedisProvider) ListAllResourceDefinitions() (map[string][]*ResourceDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	namespaces := rp.namespacesLocked()
	result := make(map[string][]*ResourceDefinition, len(namespaces))
	for _, ns := range namespaces {
		result[ns] = definitionsOf(listLayered(rp.resourceDefinitions, rp.builtinResources, ns))
	}
	return result, nil
}

// GetRelationDefinition 获取关联定义，按 业务 namespace → __all__ → 内置 查找
func (rp *RedisProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	if def, ok := lookupLayered(rp.relationDefinitions, rp.builtinRelations, rp.normalizeNamespace(namespace), name); ok {
		return def, nil
	}
	rp.config.Logger.Debugf("relation definition not found: namespace=%s, name=%s", namespace, name)
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationDefinitions 列出指定命名空间下生效的关联定义：
// 业务 namespace、__all__ 与内置按查找顺序合并，同名只取上层整条定义
func (rp *RedisProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return definitionsOf(listLayered(rp.relationDefinitions, rp.builtinRelations, rp.normalizeNamespace(namespace))), nil
}

// ListAllRelationDefinitions 返回 __all__ 与覆盖层各 namespace 生效的关联定义，按 namespace 分组
func (rp *RedisProvider) ListAllRelationDefinitions() (map[string][]*RelationDefinition, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	namespaces := rp.namespacesLocked()
	result := make(map[string][]*RelationDefinition, len(namespaces))
	for _, ns := range namespaces {
		result[ns] = definitionsOf(listLayered(rp.relationDefinitions, rp.builtinRelations, ns))
	}
	return result, nil
}

// namespacesLocked 返回 __all__ 与覆盖层里的全部 namespace，__all__ 在前，调用方需持有 rp.mu。
func (rp *RedisProvider) namespacesLocked() []string {
	return append([]string{NamespaceAll}, rp.businessNamespacesLocked()...)
}

// GetResourcePrimaryKeys 获取资源类型的主键字段列表，先查 __all__ 的生效定义，再按名字顺序查业务 namespace
func (rp *RedisProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, ns := range rp.namespacesLocked() {
		for _, rd := range definitionsOf(listLayered(rp.resourceDefinitions, rp.builtinResources, ns)) {
			if ToResourceType(rd) == resourceType {
				return rd.GetPrimaryKeys()
			}
		}
	}
	return []string{}
}

// GetRelationSchema 获取关联关系的 Schema，查找顺序与 GetResourcePrimaryKeys 相同
func (rp *RedisProvider) GetRelationSchema(relationType RelationName) (*RelationSchema, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, ns := range rp.namespacesLocked() {
		for _, rd := range definitionsOf(listLayered(rp.relationDefinitions, rp.builtinRelations, ns)) {
			if ToRelationName(rd) == relationType {
				schema := ToRelationSchema(rd)
				return &schema, nil
			}
		}
	}
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出 __all__ 的生效关联 Schema 与各业务 namespace 在 Redis 里的关联 Schema。
// 不区分 namespace：业务 namespace 与 __all__ 的同名定义各列一次，RelationName 带各自的 namespace 前缀。
func (rp *RedisProvider) ListRelationSchemas() []RelationSchema {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make([]RelationSchema, 0, len(rp.builtinRelations))
	for _, rd := range definitionsOf(listLayered(rp.relationDefinitions, rp.builtinRelations, NamespaceAll)) {
		result = append(result, ToRelationSchema(rd))
	}
	for _, ns := range rp.businessNamespacesLocked() {
		names := make([]string, 0, len(rp.relationDefinitions[ns]))
		for name := range rp.relationDefinitions[ns] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			result = append(result, ToRelationSchema(rp.relationDefinitions[ns][name]))
		}
	}
	return result
}

// EffectiveSchema 返回 namespace 的生效定义及每条定义的来源
func (rp *RedisProvider) EffectiveSchema(namespace string) EffectiveSchema {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	ns := rp.normalizeNamespace(namespace)
	return EffectiveSchema{
		Provider:  rp.Name(),
		Namespace: ns,
		Lookup:    lookupOrder(ns),
		Overlay:   rp.OverlayStatus(),
		Resources: effectiveDefinitions(listLayered(rp.resourceDefinitions, rp.builtinResources, ns), SameResourceDefinition),
		Relations: effectiveDefinitions(listLayered(rp.relationDefinitions, rp.builtinRelations, ns), SameRelationDefinition),
	}
}

// OverlayStatus 返回 Redis 覆盖层的读取状态
func (rp *RedisProvider) OverlayStatus() OverlayStatus {
	rp.statusMu.Lock()
	defer rp.statusMu.Unlock()
	return rp.status.snapshot()
}

func (rp *RedisProvider) recordSuccess() {
	rp.statusMu.Lock()
	defer rp.statusMu.Unlock()
	rp.status.lastSuccess = time.Now()
}

func (rp *RedisProvider) recordFailure(reason string, err error) {
	rp.statusMu.Lock()
	defer rp.statusMu.Unlock()
	rp.status.recordFailure(reason, err)
}

// SourceCounts 统计生效定义的来源
func (rp *RedisProvider) SourceCounts() []SourceCount {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	return append(countSources(KindResourceDefinition, rp.resourceDefinitions, rp.builtinResources),
		countSources(KindRelationDefinition, rp.relationDefinitions, rp.builtinRelations)...)
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

// ListNamespaces returns __all__ and every namespace that has definitions in Redis
func (rp *RedisProvider) ListNamespaces() ([]string, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return rp.namespacesLocked(), nil
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
var (
	_ SchemaProvider        = (*RedisProvider)(nil)
	_ SchemaSourceInspector = (*RedisProvider)(nil)
)
