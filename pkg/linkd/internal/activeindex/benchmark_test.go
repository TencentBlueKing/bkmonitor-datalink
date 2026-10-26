// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activeindex

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"linkd/internal/domain"
)

// BenchmarkRefreshStrategies 衡量同租户 16 个策略、128 条告警的一轮完整校准。
func BenchmarkRefreshStrategies(b *testing.B) {
	reader := &fakeReader{}
	for i := range 128 {
		strategy := fmt.Sprint(i % 16)
		row := activeRow("source-a", "tenant", strategy, fmt.Sprint(i))
		patches := make([]domain.EnrichPatch, 17)
		for j := range 16 {
			patches[j] = domain.EnrichPatch{Op: "set", Path: fmt.Sprintf("$.extra_data.field_%d", j), Value: json.RawMessage(`{"name":"sample","nested":{"values":[1,2,3],"enabled":true}}`)}
		}
		patches[16] = domain.EnrichPatch{Op: "set", Path: "$.labels.strategy_id", Value: json.RawMessage(strategy)}
		raw, err := json.Marshal([]map[string]any{{"fields": map[string]any{"status": "succeeded", "patches": patches}}})
		if err != nil {
			b.Fatal(err)
		}
		row.Enrich = domain.JSONObject{"processors": raw}
		reader.rows = append(reader.rows, row)
	}
	cache := &fakeCache{values: map[Scope][]string{}, pending: map[Scope]string{}}
	manager, err := NewManager(reader, cache, []string{"source-a"}, Settings{PollInterval: time.Second, ReconcileInterval: time.Minute, OperationTimeout: time.Minute, PageSize: 1000, MaxBytes: 1 << 20}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	manager.nextDiscovery = time.Now().Add(time.Hour)
	b.ReportAllocs()
	for b.Loop() {
		for i := range 16 {
			cache.pending[Scope{"tenant", fmt.Sprint(i)}] = "token"
		}
		if err := manager.Step(b.Context()); err != nil {
			b.Fatal(err)
		}
		if len(cache.pending) != 0 || cache.failures != 0 {
			b.Fatal("refresh failed")
		}
	}
}
