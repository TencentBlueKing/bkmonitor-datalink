//go:build integration

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package relation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func startSchemaRedis(t *testing.T) *redis.Client {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("integration test requires redis-server")
	}
	// 缩短目录名，给指向工作区的 TMPDIR 留出 Unix socket 路径长度空间。
	dir, err := os.MkdirTemp(os.TempDir(), "schema-redis-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket,
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
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
	client := redis.NewClient(&redis.Options{
		Network: "unix", Addr: socket, MaxRetries: -1,
		DialTimeout: 100 * time.Millisecond, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.Eventually(t, func() bool {
		return client.Ping(context.Background()).Err() == nil
	}, 5*time.Second, 10*time.Millisecond, "local Redis did not start; log: %s", log.Name())
	return client
}

func TestRedisProviderNamespaceRefreshReadinessIntegration(t *testing.T) {
	client := startSchemaRedis(t)
	definitions := map[string]string{
		KindResourceDefinition: `{"service":{"namespace":"__all__","name":"service","fields":[{"name":"id","required":true}]}}`,
		KindRelationDefinition: `{"calls":{"namespace":"__all__","name":"calls","from_resource":"service","to_resource":"service"}}`,
	}
	for _, kind := range []string{KindResourceDefinition, KindRelationDefinition} {
		for _, change := range []string{"malformed JSON", "read failure", "missing namespace", "unchanged"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for definitionKind, value := range definitions {
					key := RedisKeyPrefix + ":" + definitionKind
					require.NoError(t, client.Del(ctx, key).Err())
					require.NoError(t, client.HSet(ctx, key, NamespaceAll, value).Err())
				}
				require.NoError(t, client.Set(ctx, RelationSchemaReadyKey, `{"version":"v1"}`, 0).Err())
				provider, err := NewRedisProvider(ctx, client, WithReconcileInterval(0))
				require.NoError(t, err)
				defer provider.Close()
				require.NoError(t, provider.EnsureVersion(ctx, "v1"))

				changes := make(chan string, 1)
				require.NoError(t, provider.Subscribe(func(kind, namespace string) { changes <- kind }))
				channel := RedisKeyPrefix + ":" + kind + DefaultRedisPubSubChannelSuffix
				require.Eventually(t, func() bool {
					counts, err := client.PubSubNumSub(ctx, channel).Result()
					return err == nil && counts[channel] == 1
				}, time.Second, time.Millisecond)
				announce := func() {
					t.Helper()
					payload, err := json.Marshal(MsgPayload{Namespace: NamespaceAll, Kind: kind})
					require.NoError(t, err)
					require.NoError(t, client.Publish(ctx, channel, payload).Err())
					select {
					case received := <-changes:
						require.Equal(t, kind, received)
					case <-ctx.Done():
						t.Fatal("namespace refresh did not finish:", ctx.Err())
					}
				}

				key := RedisKeyPrefix + ":" + kind
				var expectedError string
				switch change {
				case "malformed JSON":
					require.NoError(t, client.HSet(ctx, key, NamespaceAll, "{").Err())
					expectedError = "invalid relation schema snapshot"
				case "read failure":
					require.NoError(t, client.Del(ctx, key).Err())
					require.NoError(t, client.Set(ctx, key, "not a hash", 0).Err())
					expectedError = "WRONGTYPE"
				case "missing namespace":
					require.NoError(t, client.HDel(ctx, key, NamespaceAll).Err())
					expectedError = "incomplete"
				}
				announce()
				if expectedError == "" {
					require.NoError(t, provider.EnsureVersion(ctx, "v1"))
				} else {
					require.ErrorContains(t, provider.EnsureVersion(ctx, "v1"), expectedError)
				}
				if change == "malformed JSON" || change == "read failure" {
					// 刷新失败后，旧查询仍可读取原快照，但不能继续据此判定就绪。
					resource, err := provider.GetResourceDefinition(NamespaceAll, "service")
					require.NoError(t, err)
					require.Equal(t, "id", resource.Fields[0].Name)
					relation, err := provider.GetRelationDefinition(NamespaceAll, "calls")
					require.NoError(t, err)
					require.Equal(t, "service", relation.FromResource)
				}

				require.NoError(t, client.Del(ctx, key).Err())
				require.NoError(t, client.HSet(ctx, key, NamespaceAll, definitions[kind]).Err())
				announce()
				require.NoError(t, provider.EnsureVersion(ctx, "v1"), "restored snapshot must be usable")

				// 未知 kind 的无关 hash 解码失败，不应使已验证的快照失效。
				require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":UnknownDefinition", NamespaceAll, "{").Err())
				require.Error(t, provider.reloadNamespace("UnknownDefinition", NamespaceAll))
				provider.mu.RLock()
				version := provider.ensuredVersion
				provider.mu.RUnlock()
				require.Equal(t, "v1", version)
			})
		}
	}
}
