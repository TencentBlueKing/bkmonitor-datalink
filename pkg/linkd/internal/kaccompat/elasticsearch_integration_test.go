// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kaccompat

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"linkd/internal/config"
)

// TestKACCompatibilityElasticsearch 使用真实 ES 验证原 mapping、轮转定位、处置字段及重启后继续更新。
// 显式启用且只创建/清理本测试独有的 alias、索引、模板、ILM 和同步元数据。
func TestKACCompatibilityElasticsearch(t *testing.T) {
	address := os.Getenv("LINKD_TEST_KAC_ELASTICSEARCH_URL")
	if address == "" {
		t.Skip("set LINKD_TEST_KAC_ELASTICSEARCH_URL for integration test")
	}
	zero := 0
	alias := fmt.Sprintf("linkd-kac-it-%d", time.Now().UnixNano())
	cfg := config.KACPluginConfig{Enabled: true, AlarmEventIndex: alias, ActionEndpoint: "http://unused.example/action", JWT: config.JWTConfig{SecretKey: "test-token"}, Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{address}, NumberOfShards: 1, NumberOfReplicas: &zero}}
	level := func(s string) (string, error) { return s, nil }
	c, err := Open(cfg, level, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, path := range []string{"/" + alias + "-000001", "/" + alias + "-000002", "/" + c.stateIndex, "/_template/" + alias, "/_ilm/policy/" + alias + "_policy"} {
			if err := c.request(ctx, http.MethodDelete, path, nil, nil, nil); err != nil && !hasStatus(err, 404) {
				t.Error("cleanup isolated resource", err)
			}
		}
		c.Close()
	})
	if err = c.Maintain(t.Context()); err != nil {
		t.Fatal("initial schema", err)
	}
	_, _, a := fixture(t)
	first := project(t, c, a)
	index := alias + "-000001"
	patch := map[string]any{"doc": map[string]any{"status": "executing", "conductor": []string{"operator"}, "notify_status": "success", "field_extra_info": map[string]any{"strategy_name": map[string]any{"snapshot_id": "external-snapshot"}}}}
	if err = c.request(t.Context(), http.MethodPost, "/"+index+"/_update/"+url.PathEscape(first.AlarmID), nil, patch, nil); err != nil {
		t.Fatal(err)
	}
	if err = c.request(t.Context(), http.MethodPost, "/"+alias+"/_rollover", nil, map[string]any{}, nil); err != nil {
		t.Fatal("rollover", err)
	}
	if err = c.Maintain(t.Context()); err != nil {
		t.Fatal("idempotent maintenance after dynamic object mapping", err)
	}
	reopened, err := Open(cfg, level, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	advance(&a)
	a.Severity = "critical"
	next := project(t, reopened, a)
	if first.DocumentRef != next.DocumentRef {
		t.Fatal("rollover/restart relocated compatibility document")
	}
	doc, err := c.getAlarm(t.Context(), index, first.AlarmID)
	if err != nil || doc.Source["status"] != "executing" || doc.Source["level"] != "critical" || doc.Source["notify_status"] != "success" {
		t.Fatal("partial update lost state", err)
	}
	extra := doc.Source["field_extra_info"].(map[string]any)["strategy_name"].(map[string]any)
	if extra["snapshot_id"] != "external-snapshot" {
		t.Fatal("nested KAC field lost")
	}
	if _, err = c.getAlarm(t.Context(), alias+"-000002", first.AlarmID); !hasStatus(err, 404) {
		t.Fatal("duplicate compatibility document after rollover", err)
	}
	if err = c.Maintain(t.Context()); err != nil {
		t.Fatal("maintenance changed live mapping", err)
	}
}
