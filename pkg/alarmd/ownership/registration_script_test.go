// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

const registrationScriptPrefix = "alarmd-registration"

func registrationStore(t *testing.T, client *redis.Client) *RedisStore {
	t.Helper()
	store, err := NewRedisStoreWithClient(client, registrationScriptPrefix)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A Worker's registration script on a real server, as RegisterWorker calls
// it: the registration with its life beside its registry entry scored by
// its expiry, on a cold, warm and flushed script cache, and nothing written
// when the server refuses it.
func TestRegisterWorkerScriptRunsAsTheStoreCallsIt(t *testing.T) {
	var expires time.Time
	redistest.CheckScriptSite(t, redistest.ScriptSite{
		Call: func(client *redis.Client) error {
			expires = time.Now().Add(time.Minute).Truncate(time.Millisecond)
			return registrationStore(t, client).RegisterWorker(context.Background(), WorkerRegistration{WorkerID: "worker-a",
				AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy, DeploymentProfile: "standard",
				CapabilitiesDigest: "caps", ExecutionContractDigest: "execution-v1", ExpiresAt: expires})
		},
		Written: func(t *testing.T, client *redis.Client) {
			ctx := context.Background()
			store := registrationStore(t, client)
			worker, found, err := store.ReadWorker(ctx, "worker-a")
			if err != nil || !found || !worker.ExpiresAt.Equal(expires) {
				t.Fatalf("ReadWorker = %+v, %v, %v", worker, found, err)
			}
			if ttl := client.PTTL(ctx, store.workerKey("worker-a")).Val(); ttl <= 0 || ttl > time.Minute {
				t.Fatalf("registration ttl %v, want its minute", ttl)
			}
			if score, err := client.ZScore(ctx, store.workerRegistryKey(), "worker-a").Result(); err != nil || score != float64(expires.UnixMilli()) {
				t.Fatalf("registry score %v, %v; want the expiry %d", score, err, expires.UnixMilli())
			}
		},
	})
}

// A preferred candidate's registration script on a real server, as
// RegisterInstance calls it: the record with its life, its entry in the
// preferred index, and the expired entries of both indexes dropped, on a
// cold, warm and flushed script cache, and nothing written when the server
// refuses it.
func TestRegisterPreferredInstanceScriptRunsAsTheStoreCallsIt(t *testing.T) {
	var expires time.Time
	redistest.CheckScriptSite(t, redistest.ScriptSite{
		Prepare: func(t *testing.T, client *redis.Client) {
			store := registrationStore(t, client)
			old := float64(time.Now().Add(-time.Hour).UnixMilli())
			for key, member := range map[string]string{store.preferredCandidatesKey(): "rolled-control", store.fallbackCandidatesKey(): "rolled-worker"} {
				if err := client.ZAdd(context.Background(), key, &redis.Z{Score: old, Member: member}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			live := float64(time.Now().Add(time.Hour).UnixMilli())
			if err := client.ZAdd(context.Background(), store.fallbackCandidatesKey(), &redis.Z{Score: live, Member: "live-worker"}).Err(); err != nil {
				t.Fatal(err)
			}
		},
		Call: func(client *redis.Client) error {
			expires = time.Now().Add(time.Minute).Truncate(time.Millisecond)
			return registrationStore(t, client).RegisterInstance(context.Background(), InstanceRegistration{InstanceID: "control-a",
				Roles: roles.Set{roles.Control}, Incarnation: "c", ExpiresAt: expires, ControlCandidacy: ControlCandidacyPreferred})
		},
		Written: func(t *testing.T, client *redis.Client) {
			ctx := context.Background()
			store := registrationStore(t, client)
			instance, found, err := store.ReadInstance(ctx, "control-a")
			if err != nil || !found || !instance.ExpiresAt.Equal(expires) {
				t.Fatalf("ReadInstance = %+v, %v, %v", instance, found, err)
			}
			if ttl := client.PTTL(ctx, store.instanceKey("control-a")).Val(); ttl <= 0 || ttl > time.Minute {
				t.Fatalf("record ttl %v, want its minute", ttl)
			}
			preferred := client.ZRangeWithScores(ctx, store.preferredCandidatesKey(), 0, -1).Val()
			if len(preferred) != 1 || preferred[0].Member != "control-a" || preferred[0].Score != float64(expires.UnixMilli()) {
				t.Fatalf("preferred index %v, want only control-a scored by its expiry", preferred)
			}
			if fallbacks := client.ZRange(ctx, store.fallbackCandidatesKey(), 0, -1).Val(); len(fallbacks) != 1 || fallbacks[0] != "live-worker" {
				t.Fatalf("fallback index %v, want the live entry only", fallbacks)
			}
		},
	})
}
