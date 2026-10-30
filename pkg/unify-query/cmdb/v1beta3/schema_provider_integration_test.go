//go:build integration

// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestEnsureSchemaVersionWithRedisIntegration(t *testing.T) {
	binary, err := exec.LookPath("redis-server")
	require.NoError(t, err, "integration test requires redis-server")
	dir, err := os.MkdirTemp(os.TempDir(), "schema-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no", "--dir", dir)
	require.NoError(t, cmd.Start())
	stopped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-stopped
	})
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.Eventually(t, func() bool { return client.Ping(ctx).Err() == nil }, time.Second, 10*time.Millisecond)
	require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+relation.KindResourceDefinition, relation.NamespaceAll, `{"service":{"name":"service","fields":[{"name":"id","required":true}]}}`).Err())
	require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+relation.KindRelationDefinition, relation.NamespaceAll, `{"calls":{"name":"calls","from_resource":"service","to_resource":"service"}}`).Err())
	require.NoError(t, client.Set(ctx, relation.RelationSchemaReadyKey, `{"version":"v1"}`, 0).Err())
	provider, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	previous := GetUnderlyingProvider(GetSchemaProvider())
	InitSchemaProvider(provider)
	t.Cleanup(func() { InitSchemaProvider(previous) })
	require.NoError(t, EnsureSchemaVersion(ctx, "v1"))
	require.Equal(t, []string{"id"}, GetSchemaProvider().GetResourcePrimaryKeys(relation.NamespaceAll, "service"))
	require.ErrorContains(t, EnsureSchemaVersion(ctx, "old"), "version changed")
	require.NoError(t, client.Set(ctx, relation.RelationSchemaReadyKey, `{"version":"v2"}`, 0).Err())
	require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+relation.KindResourceDefinition, relation.NamespaceAll, `{"service":{"name":"service","fields":[{"name":"new_id","required":true}]}}`).Err())
	require.NoError(t, EnsureSchemaVersion(ctx, "v2"))
	require.Equal(t, []string{"new_id"}, GetSchemaProvider().GetResourcePrimaryKeys(relation.NamespaceAll, "service"))
}
