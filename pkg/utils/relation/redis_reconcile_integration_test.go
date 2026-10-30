//go:build integration

// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package relation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRedisProviderReconcileSnapshotIntegration(t *testing.T) {
	client := startSchemaRedis(t)
	ctx := context.Background()
	core, logs := observer.New(zap.DebugLevel)
	provider := &RedisProvider{
		client: client, ctx: ctx, config: DefaultRedisProviderConfig(),
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
	}
	provider.config.Logger = zap.New(core).Sugar()
	changes := make(chan string, 20)
	require.NoError(t, provider.Subscribe(func(kind, namespace string) { changes <- kind + ":" + namespace }))
	expectChanges := func(want []string) {
		t.Helper()
		var got []string
		for range want {
			select {
			case change := <-changes:
				got = append(got, change)
			case <-time.After(time.Second):
				t.Fatal("schema callbacks did not finish")
			}
		}
		require.ElementsMatch(t, want, got)
		select {
		case change := <-changes:
			t.Fatalf("unexpected schema callback: %s", change)
		case <-time.After(20 * time.Millisecond):
		}
	}
	logs.TakeAll()
	require.NoError(t, provider.loadAllEntities())
	expectChanges(nil)
	require.Empty(t, logs.TakeAll())
	definitions := map[string]string{
		KindResourceDefinition: `{"service":{"name":"service"},"host":{"name":"host"}}`,
		KindRelationDefinition: `{"calls":{"name":"calls"},"hosts":{"name":"hosts"}}`,
	}
	for kind, value := range definitions {
		for _, ns := range []string{NamespaceAll, "a", "b"} {
			require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":"+kind, ns, value).Err())
		}
	}
	require.NoError(t, client.Set(ctx, RelationSchemaReadyKey, `{"version":"v1"}`, 0).Err())
	require.NoError(t, provider.EnsureVersion(ctx, "v1"))
	expectChanges([]string{
		KindResourceDefinition + ":" + NamespaceAll, KindResourceDefinition + ":a", KindResourceDefinition + ":b",
		KindRelationDefinition + ":" + NamespaceAll, KindRelationDefinition + ":a", KindRelationDefinition + ":b",
	})
	require.Len(t, logs.FilterMessageSnippet("changed_namespaces=6").All(), 1)
	for _, kind := range []string{KindResourceDefinition, KindRelationDefinition} {
		t.Run(kind, func(t *testing.T) {
			key := RedisKeyPrefix + ":" + kind
			logs.TakeAll()
			// 两个已存在的 namespace 与首次出现的 namespace 分别损坏。
			require.NoError(t, client.HSet(ctx, key, "a", "{", "b", `{"broken":true}`, "new", "{").Err())
			require.NoError(t, provider.loadAllEntities())
			expectChanges(nil)
			require.Empty(t, logs.FilterMessageSnippet("reconciled Redis definitions").All())
			require.Empty(t, provider.ensuredVersion)
			resources, err := provider.ListAllResourceDefinitions()
			require.NoError(t, err)
			relations, err := provider.ListAllRelationDefinitions()
			require.NoError(t, err)
			for _, ns := range []string{NamespaceAll, "a", "b"} {
				require.Len(t, resources[ns], 2)
				require.Len(t, relations[ns], 2)
			}
			require.NotContains(t, provider.resourceDefinitions, "new")
			require.NotContains(t, provider.relationDefinitions, "new")
			require.ErrorContains(t, provider.EnsureVersion(ctx, "v1"), "invalid relation schema snapshot")
			for _, ns := range []string{"a", "b"} {
				require.NoError(t, client.HSet(ctx, key, ns, definitions[kind]).Err())
			}
			require.NoError(t, client.HDel(ctx, key, "new").Err())
			require.NoError(t, provider.EnsureVersion(ctx, "v1"))
			logs.TakeAll()
			require.NoError(t, client.HDel(ctx, key, "a", "b").Err())
			require.NoError(t, provider.loadAllEntities())
			expectChanges([]string{kind + ":a", kind + ":b"})
			require.Len(t, logs.FilterMessageSnippet("changed_namespaces=2").All(), 1)
			for _, ns := range []string{"a", "b"} {
				require.NoError(t, client.HSet(ctx, key, ns, definitions[kind]).Err())
			}
			require.NoError(t, provider.EnsureVersion(ctx, "v1"))
			expectChanges([]string{kind + ":a", kind + ":b"})
		})
	}
}

func TestRedisProviderPeriodicReconcileIntegration(t *testing.T) {
	client := startSchemaRedis(t)
	for _, interval := range []time.Duration{-time.Second, 0, 20 * time.Millisecond} {
		t.Run(interval.String(), func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, client.FlushDB(ctx).Err())
			key := RedisKeyPrefix + ":" + KindResourceDefinition
			require.NoError(t, client.Set(ctx, key, "wrong type", 0).Err())
			core, logs := observer.New(zap.DebugLevel)
			provider, err := NewRedisProvider(ctx, client, WithReloadOnStart(false),
				WithLogger(zap.New(core).Sugar()), WithReconcileInterval(interval))
			require.NoError(t, err)
			defer provider.Close()
			require.Eventually(t, func() bool {
				return logs.FilterMessageSnippet("reconciliation after subscription failed").Len() > 0
			}, time.Second, time.Millisecond)
			if interval > 0 {
				require.Eventually(t, func() bool {
					return logs.FilterMessageSnippet("periodic Redis schema reconciliation failed").Len() > 0
				}, time.Second, time.Millisecond)
			}
			require.NoError(t, client.Del(ctx, key).Err())
			require.NoError(t, client.HSet(ctx, key, NamespaceAll, `{"service":{"name":"service"}}`).Err())
			if interval > 0 {
				require.Eventually(t, func() bool {
					_, err := provider.GetResourceDefinition(NamespaceAll, "service")
					return err == nil
				}, time.Second, time.Millisecond)
			} else {
				require.Never(t, func() bool {
					_, err := provider.GetResourceDefinition(NamespaceAll, "service")
					return err == nil
				}, 60*time.Millisecond, 5*time.Millisecond)
				require.Empty(t, logs.FilterMessageSnippet("periodic Redis").All())
			}
			require.NoError(t, provider.Close())
			require.NoError(t, provider.Close())
			require.NotNil(t, provider.ctx.Err())
		})
	}
}

func TestRedisProviderDecodeNamespaces(t *testing.T) {
	provider := &RedisProvider{config: DefaultRedisProviderConfig()}
	for _, namespace := range []string{"", NamespaceAll, "business"} {
		for _, data := range []string{`{}`, `{"first":{"name":"first"},"second":{"name":"second"}}`, `{`, `{"bad":true}`} {
			t.Run(fmt.Sprintf("%s/%s", namespace, data), func(t *testing.T) {
				expectedNamespace := namespace
				if namespace == "" {
					expectedNamespace = NamespaceAll
				}
				resources, invalidResources := provider.decodeResourceFields(map[string]string{namespace: data})
				relations, invalidRelations := provider.decodeRelationFields(map[string]string{namespace: data})
				if data == "{" || data == `{"bad":true}` {
					require.Empty(t, resources)
					require.Empty(t, relations)
					require.Contains(t, invalidResources, expectedNamespace)
					require.Contains(t, invalidRelations, expectedNamespace)
				} else {
					require.Empty(t, invalidResources)
					require.Empty(t, invalidRelations)
					require.Contains(t, resources, expectedNamespace)
					require.Contains(t, relations, expectedNamespace)
					if data != `{}` {
						require.Equal(t, "first", resources[expectedNamespace]["first"].Name)
						require.Equal(t, "second", resources[expectedNamespace]["second"].Name)
						require.Equal(t, "first", relations[expectedNamespace]["first"].Name)
						require.Equal(t, "second", relations[expectedNamespace]["second"].Name)
					}
				}
			})
		}
	}
}
