// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package deliverysource

import (
	"context"
	"errors"
	"sync"
	"testing"

	"linkd/internal/config"
)

func globalPlugin() config.PluginsConfig {
	return config.PluginsConfig{KAC: &config.KACPluginConfig{Enabled: true, AlarmEventIndex: "kac_alarm_event", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{"http://localhost:9200"}}, ActionEndpoint: "https://kac.example/action", InternalToken: "private-token"}}
}

func TestGlobalResolverAllSourcesAndTenants(t *testing.T) {
	cfg := globalPlugin()
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.KAC.InternalToken = "changed"
	var wg sync.WaitGroup
	for _, tenant := range []string{"a", "b"} {
		for _, source := range []string{"source-a", "source-b", "builtin_alarm_merge"} {
			wg.Go(func() {
				p, e := r.ResolveProjection(t.Context(), tenant, source, 7, TargetID)
				if e != nil || p.TargetID != TargetID {
					t.Error("global projection", e)
				}
				a, e := r.ResolveAction(t.Context(), tenant, source, 7, TargetID)
				if e != nil || a.InternalToken != "private-token" || a.Endpoint != "https://kac.example/action" {
					t.Error("global action", e)
				}
			})
		}
	}
	wg.Wait()
}

func TestGlobalResolverRejectsInvalidScopeAndCancellation(t *testing.T) {
	r, err := New(globalPlugin())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(config.PluginsConfig{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant, source, target string
		version                int64
	}{{"", "source", "kac", 1}, {"a", "", "kac", 1}, {"a", "source", "other", 1}, {"a", "source", "kac", 0}} {
		if _, err := r.ResolveAction(t.Context(), tc.tenant, tc.source, tc.version, tc.target); !errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid scope", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ResolveProjection(ctx, "a", "source", 1, TargetID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
