// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagedStopBeforeFirstAssignmentDoesNotStartProducer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	observed := make(chan Run, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			t.Error(err)
		}
		if len(h.Reports) > 0 {
			select {
			case observed <- h.Reports[0]:
			default:
			}
			_ = json.NewEncoder(w).Encode([]Assignment{})
			return
		}
		_ = json.NewEncoder(w).Encode([]Assignment{{Run: Run{ID: "run", PlanID: "plan", TenantID: "tenant-a", SessionID: h.SessionID, Command: Command{OperationID: "stop-command", Action: "stop"}}, Plan: Plan{Revision: 1, Config: fixture()}}})
	}))
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		done <- RunManaged(ctx, ManagedOptions{URL: server.URL, Token: "test", PollInterval: time.Millisecond})
	}()
	select {
	case r := <-observed:
		if r.Status.State != "stopped" || r.AppliedOperation != "stop-command" {
			t.Fatal(r)
		}
	case <-ctx.Done():
		t.Fatal("pending stop was lost")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("managed process leaked")
	}
}
