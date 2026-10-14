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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	es "linkd/internal/store/elasticsearch"
	"testing/synctest"
)

func TestRecoveryOnlyWritesChangedCompatibilityFields(t *testing.T) {
	c, f, a := fixture(t)
	project(t, c, a)
	var patch map[string]any
	f.before = func(req *http.Request) {
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/"+f.index+"/_update/") {
			raw, _ := io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var body struct {
				Doc map[string]any `json:"doc"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			patch = body.Doc
		}
	}
	advance(&a)
	a.Status = domain.AlertStatusRecovered
	a.EndType = domain.AlertEndTypeSource
	a.EndReason = "resolved"
	at := a.UpdateAt
	a.EndAt = &at
	project(t, c, a)
	if patch["status"] != "restored" || patch["source_alarm_status"] == nil {
		t.Fatal("state transition missing", patch)
	}
	for _, key := range []string{"content", "name", "metric_query_params", "field_extra_info"} {
		if _, ok := patch[key]; ok {
			t.Fatal("state update resent stable fields", key)
		}
	}
}

func TestRefreshBatchDoesNotCoverLaterWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f, _ := fixture(t)
		c.batch = &es.WriteBatchTransport{} // 仅打开刷新收集窗口，不运行 transport。
		var requests atomic.Int64
		firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
		f.before = func(req *http.Request) {
			if strings.HasSuffix(req.URL.Path, "/_refresh") && requests.Add(1) == 1 {
				close(firstStarted)
				select {
				case <-releaseFirst:
				case <-req.Context().Done():
				}
			}
		}
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				if err := c.refreshIndex(context.Background(), f.index); err != nil {
					t.Error(err)
				}
			})
		}
		synctest.Wait()
		time.Sleep(10 * time.Millisecond)
		<-firstStarted
		wg.Go(func() {
			if err := c.refreshIndex(context.Background(), f.index); err != nil {
				t.Error(err)
			}
		})
		synctest.Wait()
		time.Sleep(10 * time.Millisecond)
		close(releaseFirst)
		wg.Wait()
		if requests.Load() != 2 {
			t.Fatal("later write reused an earlier refresh", requests.Load())
		}
	})
}
