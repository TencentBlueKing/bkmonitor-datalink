// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisBackendCompareAndSetExactValueAndTTL(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	ctx := context.Background()
	applied, err := backend.CompareAndSet(ctx, "runtime", nil, true, []byte("v1"), time.Minute)
	if err != nil || !applied {
		t.Fatalf("create CAS = (%t, %v)", applied, err)
	}
	applied, err = backend.CompareAndSet(ctx, "runtime", []byte("wrong"), false, []byte("v2"), time.Minute)
	if err != nil || applied {
		t.Fatalf("wrong-value CAS = (%t, %v)", applied, err)
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	if ttl := client.TTL(ctx, "runtime").Val(); ttl <= 0 {
		t.Fatalf("runtime TTL = %v", ttl)
	}
	applied, err = backend.CompareAndSet(ctx, "gap", nil, true, []byte("active"), 0)
	if err != nil || !applied {
		t.Fatalf("persistent CAS = (%t, %v)", applied, err)
	}
	if ttl := client.TTL(ctx, "gap").Val(); ttl != -1 {
		t.Fatalf("gap TTL = %v, want persistent", ttl)
	}
}
