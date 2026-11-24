// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCatalogSafetyClassificationAndCoverage(t *testing.T) {
	var writes []string
	seen := map[string]bool{}
	routes := map[string]bool{}
	for _, op := range catalog() {
		if seen[op.Name] || routes[op.Method+" "+op.Path] {
			t.Fatalf("duplicate operation %s", op.Name)
		}
		seen[op.Name] = true
		routes[op.Method+" "+op.Path] = true
		if !strings.HasPrefix(op.Path, "/local-api/") {
			t.Fatalf("operation escaped Console: %s", op.Path)
		}
		if op.RequiresWrite {
			writes = append(writes, op.Name)
			if op.Impact == "" {
				t.Error("missing impact", op.Name)
			}
		}
	}
	want := []string{"alerts.close", "event-sources.apply", "event-sources.delete", "strategy-audits.cancel", "strategy-audits.start"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("unsafe operation classification: %v", writes)
	}
	for _, path := range []string{"version", "capabilities", "config", "scheduling", "dynamic-config", "runtime/processes", "runtime/cleaner", "runtime/lifecycle", "runtime/control-plane", "infrastructure/kafka", "infrastructure/redis", "infrastructure/redis/pending", "infrastructure/redis/mailboxes", "infrastructure/redis/leases", "elasticsearch/topology", "elasticsearch/performance", "metrics", "metrics/catalog", "event-sources", "event-sources/{id}", "enrich/config/{id}", "strategy-index/targets", "strategy-index/browse", "strategy-index/reconcile", "strategy-index/audits", "strategy-index/audits/{id}"} {
		if !routes["GET /local-api/"+path] {
			t.Error("missing Console route", path)
		}
	}
	for _, entity := range []string{"events", "alerts", "alert-logs"} {
		for _, suffix := range []string{"", "/stats", "/{id}"} {
			if !routes["GET /local-api/"+entity+suffix] {
				t.Error("missing entity route", entity, suffix)
			}
		}
	}
	for _, name := range []string{"enrich.preview", "onemodel.search", "onemodel.related", "onemodel.close"} {
		op, err := findOperation(name)
		if err != nil || op.RequiresWrite || op.Method != "POST" {
			t.Error("readonly POST misclassified", name)
		}
	}
}

func TestRequestShapesAndDiscoveryAreOffline(t *testing.T) {
	for _, args := range [][]string{{"api", "list"}, {"api", "describe", "alerts.close"}, {"version"}} {
		code, out, errOut := runCLI(t, "", append([]string{"--config", "/not/a/config"}, args...)...)
		if code != 0 || out == "" || errOut != "" {
			t.Fatalf("discovery requires config %v %s", args, errOut)
		}
	}
	if _, err := decodeJSON([]byte(strings.Repeat("[", 66) + strings.Repeat("]", 66))); err == nil {
		t.Fatal("deep JSON accepted")
	}
	for _, op := range catalog() {
		if len(op.BodyParams) > 0 && !slices.Contains([]int64{4096, 1 << 20}, op.BodyLimit) {
			t.Fatal("body unbounded", op.Name)
		}
		for _, param := range op.PathParams {
			if !strings.Contains(op.Path, "{"+param.Name+"}") {
				t.Error("invalid path contract", op.Name)
			}
		}
	}
}
