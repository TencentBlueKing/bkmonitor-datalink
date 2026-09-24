// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// runtimeStateUpgradeFixture is Runtime State an earlier release wrote
// through its own store into a real server: every key's exact bytes, the
// preflight that reads them, and what that release itself loaded from them.
// testdata/runtime-state-upgrade holds one per release an upgrade may start
// from: the upstream line writes the v2 envelope, 0.2.4605 the v3 frame.
// writer_test.go.txt there wrote them: copied into that release's
// pkg/alarmd/state as a _test.go file, it runs with XV_OUT naming the
// fixture and XV_SOURCE the commit.
type runtimeStateUpgradeFixture struct {
	SourceCommit string                               `json:"source_commit"`
	Prefix       string                               `json:"prefix"`
	Contract     execution.FrozenExecutionContractRef `json:"contract"`
	Items        []execution.StatePreflightItem       `json:"items"`
	Views        []json.RawMessage                    `json:"views"`
	Keys         []struct {
		Key   string `json:"key"`
		Value string `json:"value_base64"`
		TTL   int64  `json:"ttl_ms"`
	} `json:"keys"`
}

// An upgrade reads the state the release before it wrote: each series loads
// as that release loaded it -- the same status, revision, applied version,
// Levels and history -- never as a series with nothing stored, which would
// make every one of them warm up again and forget its open alerts. A format
// this build means to stop reading must turn this into a named refusal, not
// a silent miss.
func TestRuntimeStateWrittenByAnEarlierReleaseLoads(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	paths, err := filepath.Glob("testdata/runtime-state-upgrade/*.json")
	if err != nil || len(paths) < 2 {
		t.Fatalf("fixtures %v: %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture runtimeStateUpgradeFixture
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			if len(fixture.Items) == 0 || len(fixture.Views) != len(fixture.Items) || len(fixture.Keys) != len(fixture.Items) {
				t.Fatalf("fixture from %s: %d items, %d views, %d keys", fixture.SourceCommit, len(fixture.Items), len(fixture.Views), len(fixture.Keys))
			}
			address := reserveTCPAddress(t)
			startRedisServer(t, executable, address)
			backend, err := NewRedisBackend(RedisBackendOptions{Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			waitRedisReady(t, backend)
			client := redis.NewClient(&redis.Options{Addr: address})
			t.Cleanup(func() { _ = client.Close() })
			ctx := context.Background()
			for _, key := range fixture.Keys {
				value, err := base64.StdEncoding.DecodeString(key.Value)
				if err != nil || key.TTL <= 0 {
					t.Fatalf("key %s: ttl %d, %v", key.Key, key.TTL, err)
				}
				if err := client.Set(ctx, key.Key, value, time.Duration(key.TTL)*time.Millisecond).Err(); err != nil {
					t.Fatal(err)
				}
			}
			router, err := NewFixedRouter("redis", backend)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: fixture.Prefix, Router: router, MaxValueBytes: 1 << 20,
				MaxItemsPerCall: 64, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.LoadRuntime(ctx, execution.StatePreflightRequest{Contract: fixture.Contract, Items: fixture.Items})
			if err != nil {
				t.Fatalf("load of the state %s wrote: %v", fixture.SourceCommit, err)
			}
			for index, view := range loaded.Items {
				if view.Status == execution.StateMissingWarming {
					t.Errorf("series %s: written by %s, read as nothing stored", view.Identity.SeriesIdentityDigest, fixture.SourceCommit)
					continue
				}
				// Every field the earlier release loaded is compared; fields
				// this build added to the view are not in the fixture.
				var then map[string]any
				if err := json.Unmarshal(fixture.Views[index], &then); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(view)
				var now map[string]any
				_ = json.Unmarshal(encoded, &now)
				for field, want := range then {
					if !reflect.DeepEqual(now[field], want) {
						t.Errorf("series %s field %s: %s loaded %v, this build %v", view.Identity.SeriesIdentityDigest, field, fixture.SourceCommit, want, now[field])
					}
				}
			}
		})
	}
}
