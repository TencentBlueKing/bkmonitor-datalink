// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	goRedis "github.com/go-redis/redis/v8"
)

const (
	featureFlagEnabledDefinition  = `{"variations":{"on":true,"off":false},"defaultRule":{"variation":"on"}}`
	featureFlagDisabledDefinition = `{"variations":{"on":true,"off":false},"defaultRule":{"variation":"off"}}`
	featureFlagLegacyDefinition   = `{"true":true,"false":false,"default":false,"percentage":100}`
)

func TestFeatureFlagCRUD(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	defer client.Close()
	flags := NewFeatureFlagClient(client, "crud")
	key := flags.GetFeatureFlagsPath()
	if err := mr.Set(key, "{}"); err != nil {
		t.Fatal(err)
	}
	watch, err := flags.WatchFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		for range watch {
		}
	}()
	for _, step := range []struct {
		name  string
		apply func() error
		want  map[string]string
	}{
		{"add to empty snapshot", func() error { return flags.AddFeatureFlag(ctx, "alpha", []byte(featureFlagEnabledDefinition)) }, map[string]string{"alpha": featureFlagEnabledDefinition}},
		{"add preserves other flags", func() error { return flags.AddFeatureFlag(ctx, "beta", []byte(featureFlagLegacyDefinition)) }, map[string]string{"alpha": featureFlagEnabledDefinition, "beta": featureFlagLegacyDefinition}},
		{"update preserves other flags", func() error { return flags.UpdateFeatureFlag(ctx, "alpha", []byte(featureFlagDisabledDefinition)) }, map[string]string{"alpha": featureFlagDisabledDefinition, "beta": featureFlagLegacyDefinition}},
		{"delete preserves other flags", func() error { return flags.DeleteFeatureFlag(ctx, "alpha") }, map[string]string{"beta": featureFlagLegacyDefinition}},
		{"delete last flag keeps empty snapshot", func() error { return flags.DeleteFeatureFlag(ctx, "beta") }, map[string]string{}},
	} {
		t.Run(step.name, func(t *testing.T) {
			if mr.Exists(key) {
				mr.SetTTL(key, time.Hour)
			}
			if err := step.apply(); err != nil {
				t.Fatal(err)
			}
			data, err := mr.Get(key)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot) != len(step.want) {
				t.Fatalf("unexpected flags: %s", data)
			}
			for name, definition := range step.want {
				if string(snapshot[name]) != definition {
					t.Fatalf("unexpected definition for %q: %s", name, snapshot[name])
				}
			}
			if len(step.want) == 0 && data != "{}" {
				t.Fatalf("expected an empty object after deleting the last flag, got %s", data)
			}
			if mr.TTL(key) != 0 {
				t.Fatal("successful change must persist without TTL")
			}
			select {
			case message := <-watch:
				if notification, ok := message.(*goRedis.Message); !ok || notification.Payload != data {
					t.Fatalf("expected the committed snapshot notification, got %#v", message)
				}
			case <-time.After(time.Second):
				t.Fatal("expected successful change to notify readers")
			}
		})
	}
}

func TestFeatureFlagCRUDRejectsInvalidChanges(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	defer client.Close()
	flags := NewFeatureFlagClient(client, "crud")
	missing := NewFeatureFlagClient(client, "missing")
	key := flags.GetFeatureFlagsPath()
	initial := `{"alpha":` + featureFlagEnabledDefinition + `}`
	if err := mr.Set(key, initial); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(key, time.Hour)
	var published atomic.Int32
	mr.Server().SetPreHook(func(_ *server.Peer, command string, _ ...string) bool {
		if command == "PUBLISH" {
			published.Add(1)
		}
		return false
	})
	invalidDefinition := []byte(`{"variations":{"on":true}}`)
	for _, test := range []struct {
		name  string
		apply func() error
	}{
		{"duplicate add", func() error { return flags.AddFeatureFlag(ctx, "alpha", []byte(featureFlagDisabledDefinition)) }},
		{"update missing flag", func() error { return flags.UpdateFeatureFlag(ctx, "absent", []byte(featureFlagEnabledDefinition)) }},
		{"delete missing flag", func() error { return flags.DeleteFeatureFlag(ctx, "absent") }},
		{"update missing snapshot", func() error { return missing.UpdateFeatureFlag(ctx, "alpha", []byte(featureFlagEnabledDefinition)) }},
		{"delete missing snapshot", func() error { return missing.DeleteFeatureFlag(ctx, "alpha") }},
		{"add invalid definition", func() error { return flags.AddFeatureFlag(ctx, "invalid", invalidDefinition) }},
		{"update invalid definition", func() error { return flags.UpdateFeatureFlag(ctx, "alpha", invalidDefinition) }},
		{"empty name", func() error { return flags.AddFeatureFlag(ctx, "", []byte(featureFlagEnabledDefinition)) }},
		{"uninitialized client", func() error {
			return NewFeatureFlagClient(nil, "crud").AddFeatureFlag(ctx, "alpha", []byte(featureFlagEnabledDefinition))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.apply(); err == nil {
				t.Fatal("expected invalid change to fail")
			}
			if data, err := mr.Get(key); err != nil || data != initial || mr.TTL(key) != time.Hour {
				t.Fatalf("failed change modified the snapshot: %s, error %v", data, err)
			}
		})
	}
	err := missing.AddFeatureFlag(ctx, "alpha", []byte(featureFlagEnabledDefinition))
	if err == nil || !strings.Contains(err.Error(), "snapshot does not exist; wait for UQ migration or initialize with set-feature-flags") {
		t.Fatalf("expected missing snapshot to require migration or explicit initialization, got %v", err)
	}
	if mr.Exists(missing.GetFeatureFlagsPath()) || published.Load() != 0 {
		t.Fatal("failed changes must not create a snapshot or publish notifications")
	}
}

func TestFeatureFlagCRUDReportsConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	defer client.Close()
	flags := NewFeatureFlagClient(client, "crud")
	key := flags.GetFeatureFlagsPath()
	if err := mr.Set(key, `{"alpha":`+featureFlagEnabledDefinition+`}`); err != nil {
		t.Fatal(err)
	}
	concurrent := `{"alpha":` + featureFlagEnabledDefinition + `,"concurrent":` + featureFlagDisabledDefinition + `}`
	var transactions, published atomic.Int32
	mr.Server().SetPreHook(func(peer *server.Peer, command string, _ ...string) bool {
		if command == "MULTI" {
			transactions.Add(1)
			// 在 WATCH/GET 之后、EXEC 之前模拟另一个写入者提交新快照。
			if err := mr.Set(key, concurrent); err != nil {
				peer.WriteError("ERR concurrent test write failed")
				return true
			}
		}
		if command == "PUBLISH" {
			published.Add(1)
		}
		return false
	})
	err := flags.UpdateFeatureFlag(ctx, "alpha", []byte(featureFlagDisabledDefinition))
	if !errors.Is(err, goRedis.TxFailedErr) || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("expected a retryable transaction conflict, got %v", err)
	}
	if data, err := mr.Get(key); err != nil || data != concurrent {
		t.Fatalf("concurrent write was overwritten: %s, error %v", data, err)
	}
	if transactions.Load() != 1 || published.Load() != 0 {
		t.Fatal("conflicting change must not retry automatically or publish")
	}
}
