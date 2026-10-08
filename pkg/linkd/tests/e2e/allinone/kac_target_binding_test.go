// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/projection"
)

// TestAllInOneKACTargetBindingE2E 验证普通来源无需任何 KAC 配置即可跨租户使用全局插件。
// 两种 Linkd Repository 都实际写入独立 KAC ES 索引，处置接收端为协议模拟。
func TestAllInOneKACTargetBindingE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			receiver := &kacDeliveryReceiver{t: t, projections: map[string]projection.Receipt{}, actions: map[string]actiondelivery.Receipt{}}
			receiver.allowProjection.Store(true)
			receiver.allowAction.Store(true)
			remote := httptest.NewServer(receiver)
			t.Cleanup(remote.Close)
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 8*time.Minute, kacPolicyCredentials(t, receiver, remote.URL))
			var alerts []domain.Alert
			for _, tenant := range []string{"bind-a", "bind-b", "never-configured"} {
				for _, source := range []string{"policy-a", "policy-b"} {
					event := h.send(tenant, source, "open-"+tenant+source, "same-fingerprint", "global plugin", "shared", "warning", "triggered")
					a := h.alert(onlyAlert(t, event), func(a domain.Alert) bool { return a.ActionPending == nil })
					if len(a.Projection.Targets) != 1 || !a.Projection.Targets["kac"].ActionEnabled {
						t.Fatal("global plugin missing", tenant, source)
					}
					alerts = append(alerts, a)
				}
			}
			h.until("all tenants and sources accepted", func() bool {
				receiver.mu.Lock()
				defer receiver.mu.Unlock()
				return len(receiver.actions) == 6 && len(receiver.projections) == 6
			})
			for _, a := range alerts {
				ended := h.send(a.BKTenantID, a.EventSourceID, "end-"+a.AlertID, "same-fingerprint", "global plugin", "shared", "warning", "resolved")
				row := h.alert(onlyAlert(t, ended), func(v domain.Alert) bool { return v.Status.Terminal() && v.ActionPending == nil })
				if row.AlertID != a.AlertID {
					t.Fatal("terminal changed lifecycle")
				}
			}
			h.until("terminal compatibility and actions", func() bool {
				receiver.mu.Lock()
				defer receiver.mu.Unlock()
				if len(receiver.actions) != 12 {
					return false
				}
				for _, a := range receiver.projectionAlerts {
					if !a.Status.Terminal() {
						return false
					}
				}
				return true
			})
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
			if receiver.wrongRoute.Load() != 0 {
				t.Fatal("unexpected projection HTTP route")
			}
		})
	}
}
