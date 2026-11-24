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
	"net/http"
	"net/url"
	"time"

	"linkd/internal/shieldcheck"
)

// waitDependencyCacheExpiry 以注入故障的时刻为上界，等旧的成功源快照超过60秒。
// 故障期间后台读取不会产生新的成功时间戳；这里不修改服务时钟或清理共享缓存。
func (h *policyHarness) waitDependencyCacheExpiry(since time.Time) {
	h.t.Helper()
	expires := since.Add(61 * time.Second)
	h.untilWithin("successful dependency source snapshot expires after injected failure", 62*time.Second, func() bool { return !time.Now().Before(expires) })
}

// waitShieldDependencyHealthy 等既有绑定在恢复后完成真实成功复查，避免新Event撞上失败缓存的重试间隔。
func (h *policyHarness) waitShieldDependencyHealthy(tenant, id string, since time.Time) {
	h.t.Helper()
	h.until("existing dependency binding uses recovered source snapshot", func() bool {
		var latest struct {
			Check *shieldcheck.Check `json:"check"`
		}
		h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts/"+url.PathEscape(id)+"/check?bk_tenant_id="+url.QueryEscape(tenant), nil, &latest)
		return latest.Check != nil && latest.Check.FinishedAt.After(since) && latest.Check.Report.Outcome == "retained" && !latest.Check.Report.Changed
	})
	h.waitWorkerDependencyRefresh()
}

// waitWorkerDependencyRefresh 在控制面已证明采用新快照后，等待测试 Worker 的刷新轮次。
// 本隔离用例覆盖剩余在途请求的 10 秒截止、5 秒退避及按秒调度，不扩大生产缓存的 60 秒期限。
// 不能用控制面的成功诊断假定所有 Worker 已同时切换，也不清缓存绕过真实传播过程。
func (h *policyHarness) waitWorkerDependencyRefresh() {
	h.t.Helper()
	workerReady := time.Now().Add(17 * time.Second)
	h.untilWithin("worker dependency refresh after control plane applies changed snapshot", 20*time.Second, func() bool { return !time.Now().Before(workerReady) })
}
