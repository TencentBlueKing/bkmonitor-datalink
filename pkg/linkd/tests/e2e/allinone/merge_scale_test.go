// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package allinone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/redisclient"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
)

// 每个子用例是真实单组合并，不通过提高调度频率、手动推进或强制refresh绕过生产路径。
func TestAllInOneMergeScaleE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	// 容量对照可运行本轮保存的基线二进制；普通用例仍构建当前工作区。
	binary := os.Getenv("LINKD_E2E_MERGE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "linkd")
		buildLinkd(t, t.Context(), root, binary)
	}
	budgets := []config.MergeControlConfig{{MemberBatchSize: 128, Concurrency: 8}}
	if value := os.Getenv("LINKD_E2E_MERGE_BATCH_SIZE"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			t.Fatal("invalid merge batch size")
		}
		budgets[0].MemberBatchSize = n
	}
	if value := os.Getenv("LINKD_E2E_MERGE_CONCURRENCY"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			t.Fatal("invalid merge concurrency")
		}
		budgets[0].Concurrency = n
	}
	if os.Getenv("LINKD_E2E_MERGE_COMPARE") == "1" {
		budgets = append(budgets, config.MergeControlConfig{MemberBatchSize: 64, Concurrency: 8}, config.MergeControlConfig{MemberBatchSize: 128, Concurrency: 16})
	}
	for _, backend := range []string{"elasticsearch", "mysql"} {
		for _, size := range []int{5000, 10000} {
			for _, budget := range budgets {
				if size == 10000 && budget != budgets[0] {
					continue
				}
				t.Run(fmt.Sprintf("%s/%d/b%d_c%d", backend, size, budget.MemberBatchSize, budget.Concurrency), func(t *testing.T) {
					h := startPolicyHarnessWithTimeout(t, root, binary, backend, 30*time.Minute, func(path string) { configureMergeScale(t, path, budget.MemberBatchSize, budget.Concurrency) })
					h.client.MaxResponseBytes = 32 << 20
					h.checkMergeScale(size)
					if err := h.process.stop(); err != nil {
						t.Fatal(err)
					}
					h.checkActionMessages()
					h.saveScaleBatchSpans()
				})
			}
		}
	}
}

func configureMergeScale(t *testing.T, path string, batch, concurrency int) {
	t.Helper()
	//nolint:gosec // 只读本用例生成的隔离配置。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["control_plane"] == nil {
		document["control_plane"] = map[string]any{}
	}
	document["control_plane"].(map[string]any)["merge"] = map[string]any{"member_batch_size": batch, "concurrency": concurrency}
	if os.Getenv("LINKD_E2E_MERGE_PROFILE") == "1" {
		configureScaleObservation(t, document)
	}
	raw, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

const scaleTenant = "merge-scale"

func (h *policyHarness) scaleBurst(first, last, size int, action string) {
	h.t.Helper()
	records := make([]*kgo.Record, 0, last-first)
	for i := first; i < last; i++ {
		source := "policy-a"
		if i%2 == 1 {
			source = "policy-b"
		}
		title := "scale-a"
		if i == size-1 {
			title = "scale-b"
		}
		records = append(records, h.rawRecord(scaleTenant, source, fmt.Sprintf("scale-%s-%05d", action, i), fmt.Sprintf("scale-%05d", i), title, "scale-group", "warning", action))
	}
	if err := h.kafka.ProduceSync(h.ctx, records...).FirstErr(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *policyHarness) scaleWait(description string, ready func() bool) {
	h.t.Helper()
	deadline, cancel := context.WithTimeout(h.ctx, 12*time.Minute)
	defer cancel()
	for {
		if ready() {
			return
		}
		if exited, err := h.process.checkExited(); exited {
			h.t.Fatalf("%s: process exited: %v", description, err)
		}
		select {
		case <-deadline.Done():
			h.t.Fatalf("%s: %v", description, deadline.Err())
		case <-time.After(time.Second):
		}
	}
}

// 按来源分页读最终事实，不让测试本身通过10000条搜索上限截断父或子。
func (h *policyHarness) scaleAlerts(source string) []domain.Alert {
	h.t.Helper()
	var result []domain.Alert
	after := ""
	for {
		var page struct {
			Items []domain.Alert `json:"items"`
			Next  string         `json:"next"`
		}
		h.call(http.MethodGet, "/api/v1/alerts?bk_tenant_id="+scaleTenant+"&event_source_id="+source+"&limit=32&after="+url.QueryEscape(after), nil, &page)
		result = append(result, page.Items...)
		if page.Next == "" {
			return result
		}
		after = page.Next
	}
}

func (h *policyHarness) checkMergeScale(size int) {
	h.t.Helper()
	// 1. 先填满第一条件组，直接只读Redis元信息确认全部提交，再补齐最后条件。
	h.publish(scaleTenant, policy.Merge, mergeFixture("scale-a", "scale-b", "scale-parent", 1800, false), time.Now().Add(30*time.Minute))
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	live, err := storeassembly.OpenExisting(h.ctx, *cfg.Storage, 1)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := live.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	var durableReady, admissionReady time.Duration
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	rc, err := redisclient.New(options)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	state, err := redisstate.New(rc, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		h.t.Fatal(err)
	}
	started := time.Now()
	h.scaleBurst(0, size-1, size, "triggered")
	window := ""
	lastCount := -1
	h.scaleWait("all first-condition members committed", func() bool {
		if window == "" {
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			h.call(http.MethodGet, "/api/v1/policy-runtime/merge/windows?bk_tenant_id="+scaleTenant, nil, &page)
			if len(page.Items) == 0 {
				return false
			}
			if len(page.Items) != 1 {
				h.t.Fatalf("unexpected windows: %d", len(page.Items))
			}
			window = page.Items[0].ID
		}
		meta, found, err := state.ReadMergeWindowMeta(h.ctx, scaleTenant, window)
		if err != nil {
			h.t.Fatal(err)
		}
		if meta.CommittedCount != lastCount {
			h.t.Logf("scale %d committed=%d/%d", size, meta.CommittedCount, size-1)
			lastCount = meta.CommittedCount
		}
		return found && meta.CommittedCount == size-1
	})
	ingress := time.Since(started)
	profiling := os.Getenv("LINKD_E2E_MERGE_PROFILE") == "1"
	evidenceDir := ""
	if root := os.Getenv("LINKD_E2E_MERGE_EVIDENCE_DIR"); root != "" {
		evidenceDir = filepath.Join(root, fmt.Sprintf("%d-b%d-c%d-%s", size, cfg.MergeControlSettings().MemberBatchSize, cfg.MergeControlSettings().Concurrency, h.names.Token))
	}
	var waitCPU func()
	defer func() {
		if waitCPU != nil {
			waitCPU()
		}
	}()
	phase, phaseAt := "await_decision", time.Now()
	phaseChange := func(next string) {
		if next == phase {
			return
		}
		now := time.Now()
		h.t.Logf("SCALE_PHASE members=%d phase=%s elapsed=%s since_final=%s", size, phase, now.Sub(phaseAt).Round(time.Millisecond), now.Sub(started).Round(time.Millisecond))
		if profiling {
			h.scaleMetrics("after-"+phase, evidenceDir)
		}
		phase, phaseAt = next, now
		if profiling && next == "linking" && waitCPU == nil {
			waitCPU = h.startScaleCPU("linking", evidenceDir, 5)
		}
	}
	if profiling {
		h.scaleMetrics("before-final-condition", evidenceDir)
	}
	// 2. 观察真实后台进度，只在父完全就绪后校验全部子快照和完整关系。
	started = time.Now()
	phaseAt = started
	h.scaleBurst(size-1, size, size, "triggered")
	if os.Getenv("LINKD_E2E_MERGE_RESTART") == "1" {
		h.restartDuringMerge(size, cfg, rc)
	}
	var parent domain.Alert
	var decisionID string
	lastProgress := ""
	h.scaleWait("complete large merge", func() bool {
		var page struct {
			Items []struct {
				ID       string `json:"id"`
				ParentID string `json:"parent_alert_id"`
				Phase    string `json:"phase"`
				Capture  int    `json:"capture_offset"`
				Member   int    `json:"member_offset"`
				Count    int    `json:"member_count"`
				Finished bool   `json:"window_finished"`
				Reason   string `json:"reason_code"`
			} `json:"items"`
		}
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/decisions?bk_tenant_id="+scaleTenant, nil, &page)
		if len(page.Items) == 0 {
			return false
		}
		if len(page.Items) != 1 {
			h.t.Fatal("expected exactly one decision")
		}
		d := page.Items[0]
		if d.Phase == "releasing" || d.Phase == "failed" {
			h.t.Fatalf("merge unexpectedly rejected: phase=%s", d.Phase)
		}
		decisionID = d.ID
		stage := d.Phase
		if d.Phase == "capturing" && d.Capture == size {
			stage = "render_parent"
		}
		if profiling && d.Phase == "linking" && d.Member == 0 {
			var relations struct {
				Items []domain.MergeRelation `json:"items"`
			}
			h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations?bk_tenant_id="+scaleTenant+"&limit=1&alert_id="+url.QueryEscape(d.ParentID), nil, &relations)
			if len(relations.Items) == 0 || relations.Items[0].IndexOffset < size+1 {
				stage = "references"
			}
		}
		phaseChange(stage)
		progress := fmt.Sprintf("%s capture=%d members=%d/%d elapsed=%s", d.Phase, d.Capture, d.Member, d.Count, time.Since(started).Round(time.Millisecond))
		if progress != lastProgress {
			h.t.Logf("scale %d %s", size, progress)
			lastProgress = progress
		}
		if d.Count != size {
			h.t.Fatalf("member truncation: got=%d want=%d", d.Count, size)
		}
		if d.Phase != "completed" || !d.Finished {
			return false
		}
		if durableReady == 0 {
			reader := live.Repository.GetAlert
			if realtime, ok := live.Repository.(store.LifecycleAlertStore); ok {
				reader = realtime.GetAlertCurrent
			}
			current, err := reader(h.ctx, scaleTenant, d.ParentID)
			if err != nil {
				h.t.Fatal(err)
			}
			if current.Alert.Merge != nil && current.Alert.Merge.RelationsReady && current.Alert.MergeChange == nil && current.Alert.Admission.AdmittedAt != nil {
				durableReady = time.Since(started)
				admissionReady = current.Alert.Admission.AdmittedAt.Sub(started)
			}
		}
		parents := h.scaleAlerts(domain.BuiltinMergeEventSourceID)
		if len(parents) != 1 {
			return false
		}
		parent = parents[0]
		return parent.Status == domain.AlertStatusActive && parent.Merge != nil && parent.Merge.RelationsReady && parent.MergeChange == nil && parent.Admission.AdmittedAt != nil
	})
	creation := time.Since(started)
	h.t.Logf("SCALE_READY members=%d admission=%s observed_durable=%s search_visible=%s", size, admissionReady.Round(time.Millisecond), durableReady.Round(time.Millisecond), creation.Round(time.Millisecond))
	phaseChange("ready")
	if parent.Title != fmt.Sprintf("scale-parent %d", size) || parent.Content != fmt.Sprintf("members %d", size) {
		h.t.Fatal("incomplete parent template")
	}
	children := append(h.scaleAlerts("policy-a"), h.scaleAlerts("policy-b")...)
	if len(children) != size {
		h.t.Fatalf("children=%d want=%d", len(children), size)
	}
	for _, a := range children {
		if a.Merge == nil || len(a.Merge.RelationIDs) != 1 || a.Merge.RelationIDs[0] != decisionID || len(a.Merge.Pending) != 0 || a.MergeChange != nil || a.Admission.AdmittedAt != nil {
			h.t.Fatalf("invalid linked child %s", a.AlertID)
		}
		h.expect(a.AlertID)
	}
	var relation domain.MergeRelation
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations/"+decisionID+"?bk_tenant_id="+scaleTenant, nil, &relation)
	if len(relation.Members) != size || len(relation.WaitMemberIDs) != size || relation.IndexOffset != size+1 || relation.State != "ready" {
		h.t.Fatal("relation incomplete")
	}
	// 3. 全部真实恢复事件经过Worker，直到父恢复及关系完全结束，最后核对唯一父动作。
	if profiling {
		h.scaleMetrics("before-recovery", evidenceDir)
	}
	started = time.Now()
	phase, phaseAt = "recover_members_and_parent", started
	h.scaleBurst(0, size, size, "resolved")
	lastOffset := -1
	h.scaleWait("parent recovered and relation ended", func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations/"+decisionID+"?bk_tenant_id="+scaleTenant, nil, &relation)
		if relation.State == "ending" || relation.State == "ended" {
			phaseChange("unlink_members")
		}
		if relation.EndOffset != lastOffset {
			h.t.Logf("scale %d cleanup=%d/%d state=%s", size, relation.EndOffset, size, relation.State)
			lastOffset = relation.EndOffset
		}
		if relation.State != "ended" {
			return false
		}
		parents := h.scaleAlerts(domain.BuiltinMergeEventSourceID)
		return len(parents) == 1 && parents[0].AlertID == parent.AlertID && parents[0].Status == domain.AlertStatusRecovered && parents[0].MergeChange == nil && parents[0].PolicyChange == nil
	})
	recovery := time.Since(started)
	phaseChange("ended")
	if relation.EndOffset != size || relation.EndReason != "members_ended" {
		h.t.Fatal("incomplete relation cleanup")
	}
	children = append(h.scaleAlerts("policy-a"), h.scaleAlerts("policy-b")...)
	if len(children) != size {
		h.t.Fatal("child lost after recovery")
	}
	for _, a := range children {
		if a.Status != domain.AlertStatusRecovered || a.MergeChange != nil || a.PolicyChange != nil || a.Admission.AdmittedAt != nil {
			h.t.Fatalf("invalid recovered child %s", a.AlertID)
		}
	}
	h.checkScaleEvents(size)
	h.expect(parent.AlertID, "firing", "resolved")
	h.t.Logf("SCALE_RESULT members=%d batch=%d concurrency=%d ingress=%s creation=%s recovery_cleanup=%s", size, cfg.MergeControlSettings().MemberBatchSize, cfg.MergeControlSettings().Concurrency, ingress.Round(time.Millisecond), creation.Round(time.Millisecond), recovery.Round(time.Millisecond))
}

func (h *policyHarness) checkScaleEvents(size int) {
	h.t.Helper()
	// 测试只在全流程完成后分页扫描；每页1000避免16MiB HTTP预算和ES结果窗口上限。
	var events []storedEventView
	if h.db != nil {
		events = loadMySQLEvents(h.ctx, h.t, h.db)
	} else {
		after := []any(nil)
		for {
			query := map[string]any{"size": 1000, "query": map[string]any{"match_all": map[string]any{}}, "sort": []any{map[string]any{"_id": "asc"}}}
			if after != nil {
				query["search_after"] = after
			}
			raw, err := json.Marshal(query)
			if err != nil {
				h.t.Fatal(err)
			}
			var page struct {
				TimedOut bool `json:"timed_out"`
				Shards   struct {
					Failed int `json:"failed"`
				} `json:"_shards"`
				Hits struct {
					Hits []struct {
						Source json.RawMessage `json:"_source"`
						Sort   []any           `json:"sort"`
					} `json:"hits"`
				} `json:"hits"`
			}
			if _, err := h.es.do(h.ctx, http.MethodPost, "/"+h.names.EventIndex+"/_search", raw, &page); err != nil {
				h.t.Fatal(err)
			}
			if page.TimedOut || page.Shards.Failed != 0 {
				h.t.Fatal("incomplete Event page")
			}
			for _, hit := range page.Hits.Hits {
				var saved struct {
					domain.Event
					Processing json.RawMessage `json:"processing"`
				}
				if err := json.Unmarshal(hit.Source, &saved); err != nil {
					h.t.Fatal(err)
				}
				var e storedEventView
				e.Event = saved.Event
				if err := json.Unmarshal(saved.Processing, &e.Processing); err != nil {
					h.t.Fatal(err)
				}
				events = append(events, e)
			}
			if len(page.Hits.Hits) < 1000 {
				break
			}
			after = page.Hits.Hits[len(page.Hits.Hits)-1].Sort
		}
	}
	triggered, resolved := 0, 0
	for _, e := range events {
		if e.Event.BKTenantID != scaleTenant || e.Event.EventSourceID == domain.BuiltinMergeEventSourceID {
			continue
		}
		if len(e.Event.RelatedAlertIDs) != 1 || e.Event.EnrichStatus != domain.EnrichStatusSucceeded || e.Processing.State == domain.EventProcessStateUnprocessed {
			h.t.Fatalf("incomplete scale event %s state=%s enrich=%s related=%d", e.Event.EventID, e.Processing.State, e.Event.EnrichStatus, len(e.Event.RelatedAlertIDs))
		}
		switch {
		case strings.HasPrefix(e.Event.SourceEventID, "scale-triggered-"):
			triggered++
		case strings.HasPrefix(e.Event.SourceEventID, "scale-resolved-"):
			resolved++
		}
	}
	if triggered != size || resolved != size {
		h.t.Fatalf("event counts triggered=%d resolved=%d want=%d", triggered, resolved, size)
	}
}
