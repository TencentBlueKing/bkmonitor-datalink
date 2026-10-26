// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/activeindex"
)

type countingIndexReader struct {
	*Repository
	scans, pages, rows, maxPage int
	peakHeap                    uint64
}

func (r *countingIndexReader) ScanActiveIndex(ctx context.Context, q activeindex.Query, consume func([]activeindex.Row) error) error {
	r.scans++
	return r.Repository.ScanActiveIndex(ctx, q, func(rows []activeindex.Row) error {
		r.pages++
		r.rows += len(rows)
		r.maxPage = max(r.maxPage, len(rows))
		err := consume(rows)
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		r.peakHeap = max(r.peakHeap, memory.HeapAlloc)
		return err
	})
}

// TestActiveIndexScaleIntegration 显式验证 20 万条、单策略 15 万成员的真实 ES→Redis 完整刷新。
// 使用独立索引和随机 Redis 前缀；需要额外设置 LINKD_TEST_ACTIVE_INDEX_SCALE=1。
func TestActiveIndexScaleIntegration(t *testing.T) {
	if os.Getenv("LINKD_TEST_ACTIVE_INDEX_SCALE") != "1" {
		t.Skip("LINKD_TEST_ACTIVE_INDEX_SCALE is not set to 1")
	}
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	repository, ctx := activeIndexIntegrationRepository(t)
	client := redis.NewClient(&redis.Options{Addr: address, Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"), Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), DB: 8, ContextTimeoutEnabled: true})
	prefix := "linkd-scale:" + uuid.NewString()
	cache := activeindex.NewRedisCache(client, prefix, 2000, 32<<20)
	t.Cleanup(func() {
		defer func() { _ = client.Close() }()
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, pattern := range []string{prefix + "*", activeindex.MetadataPrefix(prefix) + "*"} {
			var cursor uint64
			for {
				keys, next, err := client.Scan(cleanup, cursor, pattern, 1000).Result()
				if err != nil {
					t.Error(err)
					break
				}
				if len(keys) > 0 {
					if err := client.Unlink(cleanup, keys...).Err(); err != nil {
						t.Error(err)
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
	})
	targets, err := repository.router.ActiveAlertTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const total = 200000
	expected := map[activeindex.Scope]int{}
	for start := 0; start < total; start += 2000 {
		var body bytes.Buffer
		for i := start; i < start+2000; i++ {
			scope := activeindex.Scope{BKTenantID: "tenant-0", StrategyID: "large"}
			if i >= 150000 {
				scope = activeindex.Scope{BKTenantID: fmt.Sprintf("tenant-%d", i%4), StrategyID: fmt.Sprint(i % 127)}
			}
			expected[scope]++
			_, _ = fmt.Fprintf(&body, `{"create":{"_index":%q,"_id":"%d"}}`+"\n", targets[0], i)
			row := map[string]any{"bk_tenant_id": scope.BKTenantID, "event_source_id": "a", "status": "active", "alert_id": fmt.Sprintf("%08d", i), "fingerprint": fmt.Sprintf("%064x", i), "labels": map[string]string{"strategy_id": scope.StrategyID}}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			body.Write(raw)
			body.WriteByte('\n')
		}
		var created bulkCreateResponse
		if err := repository.performNDJSON(ctx, http.MethodPost, "/_bulk", nil, body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if len(created.Items) != 2000 {
			t.Fatalf("bulk items=%d", len(created.Items))
		}
		for _, item := range created.Items {
			if item.Create.Status != http.StatusCreated {
				t.Fatalf("bulk status=%d", item.Create.Status)
			}
		}
	}
	if err := repository.performJSON(ctx, http.MethodPost, "/"+targets[0]+"/_refresh", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	reader := &countingIndexReader{Repository: repository}
	manager, err := activeindex.NewManager(reader, cache, []string{"a"}, activeindex.Settings{PollInterval: time.Second, ReconcileInterval: time.Minute, OperationTimeout: time.Minute, PageSize: 2000, MaxBytes: 32 << 20}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// 完整刷新和内容不变的重复刷新均应逐页执行，不触发旧的十万行上限。
	for round := range 2 {
		if round == 1 {
			if err := cache.Enqueue(ctx, activeindex.Scope{BKTenantID: "tenant-0", StrategyID: "large"}); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		var baseline runtime.MemStats
		runtime.ReadMemStats(&baseline)
		reader.peakHeap = baseline.HeapAlloc
		started := time.Now()
		if err := manager.Step(ctx); err != nil {
			t.Fatal(err)
		}
		for scope, count := range expected {
			actual, err := client.SCard(ctx, scope.Key(prefix)).Result()
			if err != nil || actual != int64(count) {
				t.Fatalf("scope=%v actual=%d expected=%d error=%v", scope, actual, count, err)
			}
			status, err := cache.ReadStatus(ctx, scope)
			if err != nil || status.Error != "" || status.Members != count {
				t.Fatalf("status=%+v error=%v", status, err)
			}
		}
		t.Logf("round=%d rows=%d strategies=%d duration=%s sampled_heap_growth=%.2fMiB", round+1, total, len(expected), time.Since(started), float64(reader.peakHeap-baseline.HeapAlloc)/(1<<20))
	}
	if reader.scans != 2 || reader.pages != 200 || reader.rows != 2*total || reader.maxPage != 2000 {
		t.Fatalf("scans=%d pages=%d rows=%d max_page=%d", reader.scans, reader.pages, reader.rows, reader.maxPage)
	}
}
