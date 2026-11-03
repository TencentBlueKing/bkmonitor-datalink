// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

type navigationRedis struct {
	*fakeRedis
	keysRead, bytesRead int
}

func (client *navigationRedis) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	client.keysRead += len(keys)
	for _, key := range keys {
		client.bytesRead += len(client.values[key])
	}
	return client.fakeRedis.MGet(ctx, keys...)
}

// Uses the real store decode and service/HTTP path, excluding Redis network
// and server time. Paging does not reduce this existing whole-snapshot cost.
func navigationFixture(tb testing.TB, pods, rows int) (http.Handler, *navigationRedis, int) {
	tb.Helper()
	client := &navigationRedis{fakeRedis: newFakeRedis()}
	store, err := NewRedisStore(client, "navigation-test", time.Minute, 4<<20)
	if err != nil {
		tb.Fatal(err)
	}
	ids := make([]string, pods)
	bytes := 0
	for pod := range ids {
		ids[pod] = fmt.Sprintf("pod-%d", pod)
		snapshot := Snapshot{Replica: ids[pod], TakenAt: now, Owned: rows + 1, Determined: rows + 1, TotalAnomalies: rows}
		for row := 0; row < rows; row++ {
			item := anomaly(fmt.Sprintf("qg-%d-%d", pod, row))
			item.Replica = ids[pod]
			snapshot.Anomalies = append(snapshot.Anomalies, item)
		}
		snapshot.NoData = []Anomaly{{QueryGroup: fmt.Sprintf("qg-empty-%d", pod), Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: ids[pod]}}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			tb.Fatal(err)
		}
		if len(encoded) > 4<<20 {
			tb.Fatal("fixture exceeds snapshot budget")
		}
		client.values[store.snapshotKey(ids[pod])] = string(encoded)
		bytes += len(encoded)
	}
	service, err := NewService(stubExpectations{expectation: Expectation{Known: true, QueryGroups: pods * (rows + 1)}}, stubRegistry{replicas: ids}, store, time.Minute, func() time.Time { return now })
	if err != nil {
		tb.Fatal(err)
	}
	h, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		tb.Fatal(err)
	}
	return h, client, bytes
}

func TestNavigationDoesNotAddSnapshotReads(t *testing.T) {
	h, client, bytes := navigationFixture(t, 4, 100)
	for _, path := range []string{"/api/objects?check=NO_DATA_PERSISTENT&limit=1", "/api/objects/qg-empty-0?check=NO_DATA_PERSISTENT", "/api/objects/qg-empty-0"} {
		client.keysRead, client.bytesRead = 0, 0
		status, body := get(t, h, path)
		if status != 200 {
			t.Fatalf("%s: %d %v", path, status, body)
		}
		if client.keysRead != 4 || client.bytesRead != bytes {
			t.Fatalf("%s added reads: keys=%d bytes=%d want 4/%d", path, client.keysRead, client.bytesRead, bytes)
		}
	}
	t.Logf("each request: snapshot_keys=4 snapshot_bytes=%d", bytes)
}

func BenchmarkNavigationDetail(b *testing.B) {
	for _, shape := range []struct{ pods, rows int }{{1, 100}, {4, 100}, {4, 1000}} {
		b.Run(fmt.Sprintf("pods%d_rows%d", shape.pods, shape.rows), func(b *testing.B) {
			h, _, bytes := navigationFixture(b, shape.pods, shape.rows)
			req := httptest.NewRequest(http.MethodGet, "/api/objects/qg-0-0", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				h.ServeHTTP(response, req)
				if response.Code != 200 {
					b.Fatalf("status=%d", response.Code)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(bytes), "snapshot_bytes/op")
		})
	}
}
