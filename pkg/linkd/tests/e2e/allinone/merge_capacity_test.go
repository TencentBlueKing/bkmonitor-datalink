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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// 在生产任务预算和默认刷新周期下运行满 256 成员，度量完整推进而非只统计写入成功。
// 仅访问测试独立资源；不缩短控制面周期、不手工推进裁决、不强制刷新业务/合并索引。
func TestAllInOneMergeCapacityE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 20*time.Minute)
			h.checkMergeCapacity()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

const capacityTenant = "merge-capacity"

func (h *policyHarness) capacityBurst(first, last int, action string) {
	h.t.Helper()
	records := make([]*kgo.Record, 0, last-first)
	for i := first; i < last; i++ {
		source := "policy-a"
		if i%2 == 1 {
			source = "policy-b"
		}
		title := "capacity-a"
		if i == 255 {
			title = "capacity-b"
		}
		id := fmt.Sprintf("capacity-%s-%03d", action, i)
		records = append(records, h.rawRecord(capacityTenant, source, id, fmt.Sprintf("capacity-%03d", i), title, "capacity-group", "warning", action))
	}
	if err := h.kafka.ProduceSync(h.ctx, records...).FirstErr(); err != nil {
		h.t.Fatal(err)
	}
	lastCount := -1
	h.untilWithin("capacity events completed", 2*time.Minute, func() bool {
		count := 0
		for _, e := range h.events() {
			if e.Event.BKTenantID == capacityTenant && strings.HasPrefix(e.Event.SourceEventID, "capacity-"+action+"-") && e.Processing.State != domain.EventProcessStateUnprocessed {
				if len(e.Event.RelatedAlertIDs) != 1 || e.Event.EnrichStatus != domain.EnrichStatusSucceeded {
					h.t.Fatal("capacity Event lost completed enrichment or Alert")
				}
				count++
			}
		}
		if count != lastCount {
			h.t.Logf("capacity %s processed=%d/%d", action, count, last)
			lastCount = count
		}
		return count == last
	})
}

func (h *policyHarness) checkMergeCapacity() {
	h.t.Helper()
	h.publish(capacityTenant, policy.Merge, mergeFixture("capacity-a", "capacity-b", "capacity-parent", 1800, false), time.Now().Add(30*time.Minute))
	started := time.Now()
	h.capacityBurst(0, 255, "triggered")
	children := map[string]bool{}
	h.untilWithin("255 unique members waiting", 2*time.Minute, func() bool {
		for _, a := range h.alerts() {
			if a.BKTenantID == capacityTenant && a.EventSourceID != domain.BuiltinMergeEventSourceID {
				if a.Merge == nil || len(a.Merge.Pending) != 1 || a.Admission.AdmittedAt != nil {
					return false
				}
				children[a.AlertID] = true
			}
		}
		return len(children) == 255
	})
	h.t.Logf("capacity first 255 persisted waits in %s", time.Since(started).Round(time.Millisecond))
	started = time.Now()
	h.capacityBurst(255, 256, "triggered")
	var parent domain.Alert
	lastProgress := ""
	h.untilWithin("256-member parent fully ready", 5*time.Minute, func() bool {
		linked := 0
		for _, a := range h.alerts() {
			if a.BKTenantID != capacityTenant {
				continue
			}
			if a.EventSourceID == domain.BuiltinMergeEventSourceID {
				if a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil {
					parent = a
				}
			} else {
				children[a.AlertID] = true
				if a.Merge != nil && len(a.Merge.RelationIDs) == 1 && len(a.Merge.Pending) == 0 && a.MergeChange == nil {
					linked++
				}
				if a.Admission.AdmittedAt != nil {
					h.t.Fatal("capacity member escaped merge admission gate")
				}
			}
		}
		var decisions struct {
			Items []struct {
				Phase   string `json:"phase"`
				Capture int    `json:"capture_offset"`
			} `json:"items"`
		}
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/decisions?bk_tenant_id="+capacityTenant, nil, &decisions)
		progress := fmt.Sprintf("linked=%d parent=%t", linked, parent.AlertID != "")
		if len(decisions.Items) > 0 {
			progress = fmt.Sprintf("%s capture=%d %s", decisions.Items[0].Phase, decisions.Items[0].Capture, progress)
		}
		if progress != lastProgress {
			h.t.Log("capacity merge " + progress)
			lastProgress = progress
		}
		return parent.AlertID != "" && linked == 256 && len(children) == 256
	})
	if parent.Title != "capacity-parent 256" || parent.Content != "members 256" {
		h.t.Fatal("capacity parent template did not use complete member set")
	}
	h.t.Logf("capacity 256-member capture/render/link/admit in %s", time.Since(started).Round(time.Millisecond))
	for id := range children {
		h.expect(id)
	}
	started = time.Now()
	h.capacityBurst(0, 256, "resolved")
	h.untilWithin("all-terminal parent recovery", 2*time.Minute, func() bool {
		for _, a := range h.alerts() {
			if a.AlertID == parent.AlertID {
				return a.Status == domain.AlertStatusRecovered && a.MergeChange == nil && a.PolicyChange == nil
			}
		}
		return false
	})
	h.t.Logf("capacity 256 member recovery + parent in %s", time.Since(started).Round(time.Millisecond))
	started = time.Now()
	lastOffset := -1
	h.untilWithin("capacity relation fully ended", 10*time.Minute, func() bool {
		var relation domain.MergeRelation
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations/"+parent.Merge.RelationIDs[0]+"?bk_tenant_id="+capacityTenant, nil, &relation)
		if relation.EndOffset != lastOffset {
			h.t.Logf("capacity relation cleanup=%d/%d state=%s", relation.EndOffset, len(relation.WaitMemberIDs), relation.State)
			lastOffset = relation.EndOffset
		}
		return relation.State == "ended" && relation.EndOffset == 256 && relation.EndReason == "members_ended"
	})
	h.t.Logf("capacity relation cleanup finished in %s", time.Since(started).Round(time.Millisecond))
	h.expect(parent.AlertID, "firing", "resolved")
}
