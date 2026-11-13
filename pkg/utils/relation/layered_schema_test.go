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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func encodeDefinitions(t *testing.T, definitions any) string {
	t.Helper()
	data, err := json.Marshal(definitions)
	require.NoError(t, err)
	return string(data)
}

func loadedLayeredProvider(t *testing.T, fields map[string]map[string]string) (*RedisProvider, *snapshotRedisClient) {
	t.Helper()
	client := &snapshotRedisClient{fields: fields}
	provider := newRedisProvider(context.Background(), client, DefaultRedisProviderConfig())
	t.Cleanup(provider.cancel)
	require.NoError(t, provider.loadAllEntities())
	return provider, client
}

func sourceCount(counts []SourceCount, kind string, source DefinitionSource) int {
	for _, count := range counts {
		if count.Kind == kind && count.Source == source {
			return count.Count
		}
	}
	return -1
}

func TestLayeredSchemaSourcesAndBuiltinComparison(t *testing.T) {
	resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
	relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
	builtinResources := len(DefaultResourceDefinitions())
	builtinRelations := len(DefaultRelationDefinitions())

	empty, _ := loadedLayeredProvider(t, nil)
	counts := empty.SourceCounts()
	require.Len(t, counts, 2*len(DefinitionSources), "每个 kind 与来源的组合都要输出，缺失不等于 0")
	require.Equal(t, builtinResources, sourceCount(counts, KindResourceDefinition, DefinitionSourceBuiltin))
	require.Equal(t, builtinRelations, sourceCount(counts, KindRelationDefinition, DefinitionSourceBuiltin))
	for _, source := range []DefinitionSource{DefinitionSourceOverride, DefinitionSourceExtension, DefinitionSourceNamespace} {
		require.Zero(t, sourceCount(counts, KindResourceDefinition, source))
		require.Zero(t, sourceCount(counts, KindRelationDefinition, source))
	}
	require.Equal(t, NewDefaultStaticSchemaProvider().SourceCounts(), counts, "Redis 为空时与 static 模式的来源统计相同")

	// 一条与内置逐字段相同的副本（缺类型按内置补齐）、一条改了主键的覆盖、一条新增，外加业务 namespace 定义。
	provider, _ := loadedLayeredProvider(t, map[string]map[string]string{
		resourceKey: {
			NamespaceAll: encodeDefinitions(t, map[string]*ResourceDefinition{
				"datasource": {Namespace: NamespaceAll, Name: "datasource", Fields: []FieldDefinition{{Name: "bk_data_id", Required: true}}},
				"pod":        {Namespace: NamespaceAll, Name: "pod", Fields: []FieldDefinition{{Name: "pod", Required: true}}},
				"custom_app": {Namespace: NamespaceAll, Name: "custom_app", Fields: []FieldDefinition{{Name: "app_id", Required: true}}},
			}),
			"bkcc__2": encodeDefinitions(t, map[string]*ResourceDefinition{
				"pod": {Namespace: "bkcc__2", Name: "pod", Fields: []FieldDefinition{{Name: "pod_uid", Required: true}}},
			}),
		},
		relationKey: {
			NamespaceAll: encodeDefinitions(t, map[string]*RelationDefinition{
				"node_with_pod": {Namespace: NamespaceAll, Name: "node_with_pod", FromResource: "pod", ToResource: "node", Category: "static", IsBelongsTo: true},
				"pod_to_pod": {Namespace: NamespaceAll, Name: "pod_to_pod", FromResource: "pod", ToResource: "pod", Category: "dynamic",
					IsDirectional: true, Labels: map[string]string{"metric_name": "renamed_flow"}},
			}),
		},
	})
	counts = provider.SourceCounts()
	require.Equal(t, builtinResources-2, sourceCount(counts, KindResourceDefinition, DefinitionSourceBuiltin))
	require.Equal(t, 2, sourceCount(counts, KindResourceDefinition, DefinitionSourceOverride))
	require.Equal(t, 1, sourceCount(counts, KindResourceDefinition, DefinitionSourceExtension))
	require.Equal(t, 1, sourceCount(counts, KindResourceDefinition, DefinitionSourceNamespace))
	require.Equal(t, builtinRelations-2, sourceCount(counts, KindRelationDefinition, DefinitionSourceBuiltin))
	require.Equal(t, 2, sourceCount(counts, KindRelationDefinition, DefinitionSourceOverride))
	require.Zero(t, sourceCount(counts, KindRelationDefinition, DefinitionSourceExtension))
	require.Zero(t, sourceCount(counts, KindRelationDefinition, DefinitionSourceNamespace))

	schema := provider.EffectiveSchema("bkcc__2")
	require.Equal(t, []string{"bkcc__2", NamespaceAll, "builtin"}, schema.Lookup)
	resources := map[string]EffectiveDefinition[ResourceDefinition]{}
	for _, item := range schema.Resources {
		require.NotContains(t, resources, item.Name, "同名定义只出现一次")
		resources[item.Name] = item
	}
	require.Len(t, resources, builtinResources+1)
	require.Equal(t, DefinitionSourceNamespace, resources["pod"].Source)
	require.Equal(t, []string{"pod_uid"}, resources["pod"].Definition.GetPrimaryKeys())
	require.True(t, resources["pod"].HasBuiltin)
	require.False(t, resources["pod"].MatchesBuiltin)
	require.Equal(t, DefinitionSourceOverride, resources["datasource"].Source)
	require.True(t, resources["datasource"].MatchesBuiltin, "缺类型的字段按内置类型补齐，副本与内置一致")
	require.Equal(t, DefinitionSourceExtension, resources["custom_app"].Source)
	require.False(t, resources["custom_app"].HasBuiltin)
	require.Equal(t, DefinitionSourceBuiltin, resources["node"].Source)
	require.True(t, resources["node"].MatchesBuiltin)

	relations := map[string]EffectiveDefinition[RelationDefinition]{}
	for _, item := range provider.EffectiveSchema(NamespaceAll).Relations {
		relations[item.Name] = item
	}
	require.Equal(t, DefinitionSourceOverride, relations["node_with_pod"].Source)
	require.True(t, relations["node_with_pod"].MatchesBuiltin, "category 空与 static 等价")
	require.Equal(t, DefinitionSourceOverride, relations["pod_to_pod"].Source)
	require.False(t, relations["pod_to_pod"].MatchesBuiltin, "metric_name 改了即与内置不同")

	printed := PrintEffectiveSchema(provider.EffectiveSchema("bkcc__2"), false)
	require.Contains(t, printed, "lookup: bkcc__2 -> __all__ -> builtin\n")
	require.Contains(t, printed, fmt.Sprintf("ResourceDefinition: builtin=%d override=1 extension=1 namespace=1 differs_from_builtin=1\n", builtinResources-2))
	require.Contains(t, printed, "\npod\tnamespace\tbuiltin=differs\n")
	require.Contains(t, printed, "\ndatasource\toverride\tbuiltin=same\n")
	require.Contains(t, printed, "\ncustom_app\textension\tbuiltin=none\n")
	require.NotContains(t, printed, `"pod_uid"`)
	require.Contains(t, PrintEffectiveSchema(provider.EffectiveSchema("bkcc__2"), true), `"pod_uid"`)
}

// 业务 namespace 继承 __all__：__all__ 变了，订阅方也要收到各业务 namespace 的变更。
func TestLayeredSchemaAllChangeNotifiesBusinessNamespaces(t *testing.T) {
	resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
	relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
	provider, client := loadedLayeredProvider(t, map[string]map[string]string{
		resourceKey: {"bkcc__2": encodeDefinitions(t, map[string]*ResourceDefinition{"custom_a": {Name: "custom_a"}})},
		relationKey: {"bkcc__3": encodeDefinitions(t, map[string]*RelationDefinition{"custom_edge": {Name: "custom_edge"}})},
	})
	changes := make(chan string, 16)
	require.NoError(t, provider.Subscribe(func(kind, namespace string) { changes <- kind + ":" + namespace }))
	collect := func() []string {
		var got []string
		for {
			select {
			case change := <-changes:
				got = append(got, change)
			case <-time.After(50 * time.Millisecond):
				return got
			}
		}
	}

	client.fields[relationKey][NamespaceAll] = encodeDefinitions(t, map[string]*RelationDefinition{
		"node_with_pod": {Name: "node_with_pod", FromResource: "pod", ToResource: "node"},
	})
	require.NoError(t, provider.loadAllEntities())
	require.ElementsMatch(t, []string{
		KindRelationDefinition + ":" + NamespaceAll,
		KindRelationDefinition + ":bkcc__2",
		KindRelationDefinition + ":bkcc__3",
	}, collect())

	// 只改业务 namespace 时不扩散。
	client.fields[resourceKey]["bkcc__2"] = encodeDefinitions(t, map[string]*ResourceDefinition{"custom_b": {Name: "custom_b"}})
	require.NoError(t, provider.loadAllEntities())
	require.Equal(t, []string{KindResourceDefinition + ":bkcc__2"}, collect())
}

// 没有 Redis 客户端时 Provider 照常创建，只有内置一层，状态标为不可用。
func TestLayeredSchemaWithoutRedisClientServesBuiltin(t *testing.T) {
	ctx := context.Background()
	for name, provider := range map[string]*RedisProvider{
		"provider": NewRedisProvider(ctx, nil),
		"manager":  NewProviderManager(ctx, nil, nil, DefaultStaticProviderConfig()).GetProvider().(*RedisProvider),
	} {
		t.Run(name, func(t *testing.T) {
			defer provider.Close()
			pod, err := provider.GetResourceDefinition("bkcc__2", "pod")
			require.NoError(t, err)
			require.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"}, pod.GetPrimaryKeys())
			status := provider.OverlayStatus()
			require.Equal(t, OverlayStateUnavailable, status.State)
			require.Nil(t, status.LastSuccess)
			require.Equal(t, OverlayReasonClientMissing, status.LastReason)
			require.Equal(t, map[string]int64{OverlayReasonClientMissing: 1, OverlayReasonReadFailed: 0, OverlayReasonDecodeFailed: 0}, status.Failures)
			require.Contains(t, PrintEffectiveSchema(provider.EffectiveSchema(""), false),
				"overlay: unavailable last_success=- ")
		})
	}
	require.Equal(t, OverlayStateUnavailable, NewDefaultStaticSchemaProvider().OverlayStatus().State)
}

// 读取失败时先用内置定义，之后的全量加载成功即切到已加载；失败计数按原因累计。
func TestLayeredSchemaOverlayStatusTransitions(t *testing.T) {
	resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
	relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
	client := &snapshotRedisClient{relationErr: errors.New("connection refused"), fields: map[string]map[string]string{
		resourceKey: {NamespaceAll: encodeDefinitions(t, map[string]*ResourceDefinition{
			"custom_app": {Namespace: NamespaceAll, Name: "custom_app", Fields: []FieldDefinition{{Name: "app_id", Required: true}}},
		})},
		relationKey: {},
	}}
	provider := newRedisProvider(context.Background(), client, DefaultRedisProviderConfig())
	t.Cleanup(provider.cancel)

	require.ErrorContains(t, provider.loadAllEntities(), "connection refused")
	status := provider.OverlayStatus()
	require.Equal(t, OverlayStateUnavailable, status.State)
	require.Equal(t, OverlayReasonReadFailed, status.LastReason)
	require.Contains(t, status.LastError, "connection refused")
	require.EqualValues(t, 1, status.Failures[OverlayReasonReadFailed])
	_, err := provider.GetResourceDefinition(NamespaceAll, "pod")
	require.NoError(t, err, "读取失败期间内置定义照常可用")
	_, err = provider.GetResourceDefinition(NamespaceAll, "custom_app")
	require.ErrorIs(t, err, ErrResourceDefinitionNotFound)

	client.relationErr = nil
	client.fields[relationKey]["bkcc__2"] = "{"
	before := time.Now()
	require.NoError(t, provider.loadAllEntities())
	status = provider.OverlayStatus()
	require.Equal(t, OverlayStateLoaded, status.State)
	require.NotNil(t, status.LastSuccess)
	require.False(t, status.LastSuccess.Before(before))
	require.EqualValues(t, 1, status.Failures[OverlayReasonReadFailed])
	require.EqualValues(t, 1, status.Failures[OverlayReasonDecodeFailed], "无法解码的 namespace 单独计数，其余照常加载")
	require.Equal(t, OverlayReasonDecodeFailed, status.LastReason)
	_, err = provider.GetResourceDefinition(NamespaceAll, "custom_app")
	require.NoError(t, err)

	// 加载成功后再失败：沿用上次快照，状态仍为已加载。
	client.relationErr = errors.New("timeout")
	require.Error(t, provider.loadAllEntities())
	status = provider.OverlayStatus()
	require.Equal(t, OverlayStateLoaded, status.State)
	require.EqualValues(t, 2, status.Failures[OverlayReasonReadFailed])
	_, err = provider.GetResourceDefinition(NamespaceAll, "custom_app")
	require.NoError(t, err)

	data, err := json.Marshal(provider.EffectiveSchema(NamespaceAll))
	require.NoError(t, err)
	var decoded struct {
		Overlay struct {
			State       string           `json:"state"`
			LastSuccess string           `json:"last_success"`
			Failures    map[string]int64 `json:"failures"`
		} `json:"overlay"`
		Resources []map[string]any `json:"resources"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "loaded", decoded.Overlay.State)
	require.NotEmpty(t, decoded.Overlay.LastSuccess)
	require.EqualValues(t, 2, decoded.Overlay.Failures["read_failed"])
	require.NotEmpty(t, decoded.Resources)
	require.Contains(t, decoded.Resources[0], "source")
}

func TestLayeredSchemaWithBaseSchema(t *testing.T) {
	config := DefaultRedisProviderConfig()
	WithBaseSchema(StaticProviderConfig{ResourcePrimaryKeys: map[string][]string{"custom": {"id"}}})(config)
	provider := newRedisProvider(context.Background(), &snapshotRedisClient{}, config)
	defer provider.cancel()
	require.NoError(t, provider.loadAllEntities())
	definitions, err := provider.ListResourceDefinitions("bkcc__2")
	require.NoError(t, err)
	require.Equal(t, []string{"custom"}, resourceNames(definitions))
	require.True(t, strings.Contains(PrintEffectiveSchema(provider.EffectiveSchema(""), false), "namespace: __all__\n"))
}

func resourceNames(definitions []*ResourceDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	sort.Strings(names)
	return names
}

func relationNames(definitions []*RelationDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	sort.Strings(names)
	return names
}
