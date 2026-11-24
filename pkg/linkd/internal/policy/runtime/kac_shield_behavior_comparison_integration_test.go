// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type shieldBehaviorComparisonCase struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Mode       string `json:"mode,omitempty"`
	Model      string `json:"model,omitempty"`
	Instance   string `json:"instance,omitempty"`
	EvaluateAt int64  `json:"evaluate_at,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Mains      []struct {
		ID     string `json:"id"`
		At     int64  `json:"at"`
		Status string `json:"status"`
	} `json:"mains"`
	Child struct {
		ID       string `json:"id,omitempty"`
		At       int64  `json:"at,omitempty"`
		Title    string `json:"title,omitempty"`
		Model    string `json:"model,omitempty"`
		Instance string `json:"instance,omitempty"`
	} `json:"child"`
	Difference string                         `json:"difference,omitempty"`
	Decision   string                         `json:"decision,omitempty"`
	KAC        shieldBehaviorComparisonResult `json:"expected_kac"`
	Linkd      shieldBehaviorComparisonResult `json:"expected_linkd"`
}

type shieldBehaviorComparisonResult struct {
	Shielded bool   `json:"shielded"`
	Parent   string `json:"parent"`
}

type shieldBehaviorComparisonResponse struct {
	Sources map[string]string `json:"source_sha256"`
	Results []struct {
		ID      string                         `json:"id"`
		Result  shieldBehaviorComparisonResult `json:"result"`
		Calls   []json.RawMessage              `json:"calls"`
		Removed []string                       `json:"removed_auto_ids"`
	} `json:"results"`
}

// 固定源码记录已确认差异：Linkd 首次原子登记待处理主，目标描述只限制主候选。
func TestKACShieldSelectionBehaviorComparison(t *testing.T) {
	root := os.Getenv("LINKD_TEST_KAC_SOURCE_ROOT")
	if root == "" || os.Getenv("LINKD_TEST_REDIS_ADDRESS") == "" {
		t.Skip("set LINKD_TEST_KAC_SOURCE_ROOT and LINKD_TEST_REDIS_ADDRESS for shield source comparison")
	}
	python := os.Getenv("LINKD_TEST_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "kac_behavior_comparison", "testdata", "shield-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []shieldBehaviorComparisonCase
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	call, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	//nolint:gosec // G204: 本地显式测试配置独立传参，源码摘要先校验，不通过 shell。
	cmd := exec.CommandContext(call, python, "-B", filepath.Join("..", "..", "..", "tests", "kac_behavior_comparison", "kac_shield_behavior_comparison.py"), root)
	cmd.Env = append(os.Environ(), "PYTHONHASHSEED=0", "PYTHONDONTWRITEBYTECODE=1")
	cmd.Stdin = bytes.NewReader(raw)
	out, stderr := &sequenceOutput{limit: 1 << 20}, &sequenceOutput{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("KAC shield comparison: %v %s", err, stderr.String())
	}
	var response shieldBehaviorComparisonResponse
	decoder = json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("trailing shield response", err)
	}
	if len(response.Results) != len(cases) || len(response.Sources) != 4 {
		t.Fatal("incomplete shield comparison response")
	}
	reports := []map[string]any{}
	differences := 0
	for i, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			old := response.Results[i]
			if old.ID != tc.ID {
				t.Fatal("shield comparison identity mismatch")
			}
			current := runLinkdShieldBehaviorComparison(t, tc)
			if old.Result != tc.KAC || current != tc.Linkd {
				t.Fatalf("KAC=%+v want=%+v; Linkd=%+v want=%+v", old.Result, tc.KAC, current, tc.Linkd)
			}
			if (tc.Difference == "") != (old.Result == current) {
				t.Fatal("unclassified shield difference")
			}
			if tc.Difference != "" {
				if tc.Decision != "confirmed" {
					t.Fatal("shield difference lacks confirmed decision")
				}
				differences++
			}
			if old.Result.Shielded && len(old.Removed) == 0 {
				t.Fatal("KAC shield did not clear auto-suppression input")
			}
			reports = append(reports, map[string]any{"case": tc, "kac": old.Result, "linkd": current, "query_calls": old.Calls, "kac_removed_auto_ids": old.Removed})
		})
	}
	if t.Failed() {
		return
	}
	if path := os.Getenv("LINKD_TEST_KAC_SHIELD_REPORT"); path != "" {
		data, err := json.MarshalIndent(map[string]any{"source_sha256": response.Sources, "cases": reports, "confirmed_differences": differences}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G703: 只向本机测试执行者显式选择的路径写合成对照报告。
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("verified %d shield scenarios, including %d confirmed difference cases; Linkd uses real Redis and Memory Lifecycle", len(cases), differences)
}

func changeShieldBehaviorComparisonRelease(t *testing.T, r policy.Release, change func(map[string]any)) policy.Release {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	change(spec)
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	r.Spec, r.Compiled = c.Canonical, c.Summary
	return r
}

func shieldNameCondition(name string) map[string]any {
	return map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": name}}
}

func runLinkdShieldBehaviorComparison(t *testing.T, tc shieldBehaviorComparisonCase) shieldBehaviorComparisonResult {
	t.Helper()
	// 模型与目标夹具显式对应：普通依赖为主机，CMDB 依赖为交换机到关联主机。
	mainModel, mainInstance := "cw-Host", "101"
	mode := "custom_shield"
	if tc.Mode == "cmdb_shield" {
		mode, mainModel, mainInstance = tc.Mode, "cw-Switch", "sw1"
	}
	if tc.Child.ID != "" || (tc.Mode != "" && tc.Mode != "cmdb_shield") ||
		(tc.Model != "" && tc.Model != mainModel) || (tc.Instance != "" && tc.Instance != mainInstance) ||
		(tc.Child.Model != "" && tc.Child.Model != "cw-Host") {
		t.Fatal("unexpected model fixture")
	}
	repo := memory.New()
	clock := &policyTestClock{at: comparisonAt(tc.EvaluateAt)}
	stateHook, action := &shieldHook{}, &shieldHook{}
	enabled := tc.Enabled == nil || *tc.Enabled
	release := timeShieldRelease(t)
	if tc.Kind == "rely_shield" {
		release = dependencyRelease(t, mode)
	}
	release = changeShieldBehaviorComparisonRelease(t, release, func(spec map[string]any) {
		spec["is_enable"], spec["timezone"] = enabled, "UTC"
		name := "child"
		if tc.Kind == "rely_shield" {
			name = "main"
			rely := shieldNameCondition("child")
			if mode == "cmdb_shield" {
				rely["expression"] = "A AND B"
				rely["B"] = map[string]any{"condition": "term", "target_key": "model_id", "target_value": "cw-Host", "bk_obj_asst_id": "switch_connect_host"}
			}
			spec["rely_policy"] = rely
		}
		spec["policy"] = shieldNameCondition(name)
	})
	directory := &shieldDirectory{releases: []policy.Release{release}}
	hasPending := false
	for _, main := range tc.Mains {
		hasPending = hasPending || main.Status == "received"
	}
	if hasPending {
		// 登记后的主由第二条时间策略阻止准入，以实际验证后续子使用固定的待处理主。
		block := changeShieldBehaviorComparisonRelease(t, timeShieldRelease(t), func(spec map[string]any) { spec["policy"] = shieldNameCondition("main") })
		directory.releases = append(directory.releases, block)
	}
	processor, shielder := newShieldProcessor(t, repo, clock, directory, stateHook, action)
	shielder.Dependency = realSuppressionState(t)
	shielder.Candidates = repo
	shielder.CurrentAlert = repo.GetAlert
	relations := &dependencyRelations{}
	if mode == "cmdb_shield" {
		shielder.Relations = relations
	}
	aliases := map[string]string{}
	for _, main := range tc.Mains {
		if main.Status != "active" {
			continue
		}
		a := storetest.Alert("tenant", main.ID, "opening-"+main.ID, "fp-"+main.ID, "warning")
		a.Title = "main"
		a.Labels["model_id"] = domain.NewStringScalar(mainModel)
		a.Labels["model_inst_id"] = domain.NewStringScalar(mainInstance)
		a.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
		a.BeginAt, a.CreateAt, a.UpdateAt, a.LastOccurredAt = comparisonAt(main.At), comparisonAt(main.At), comparisonAt(main.At), comparisonAt(main.At)
		a.Admission = domain.AlertAdmission{AdmittedAt: &a.UpdateAt, Severity: a.Severity, CauseType: "source_event", CauseID: a.TriggerEventID}
		created, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		aliases[created.Alert.AlertID] = main.ID
	}
	for _, main := range tc.Mains {
		if main.Status != "received" {
			continue
		}
		event := dependencyEvent(main.ID, "source-"+main.ID, "main", clock)
		event.Labels["model_id"] = domain.NewStringScalar(mainModel)
		event.Labels["model_inst_id"] = domain.NewStringScalar(mainInstance)
		event.OccurredAt = comparisonAt(main.At)
		result := mustProcessAggregation(t, repo, processor, event)
		if len(result.Event.RelatedAlertIDs) != 1 {
			t.Fatal("pending main missing Alert")
		}
		id := result.Event.RelatedAlertIDs[0]
		aliases[id] = main.ID
		current, err := repo.GetAlert(t.Context(), "tenant", id)
		if err != nil || !current.Alert.Shield.Active || current.Alert.Admission.AdmittedAt != nil {
			t.Fatal("main fixture was not actually blocked", err)
		}
	}
	title := tc.Child.Title
	if title == "" {
		title = "child"
	}
	event := dependencyEvent("child", "child-source", title, clock)
	event.OccurredAt = comparisonAt(tc.Child.At)
	if tc.Child.Model != "" {
		event.Labels["model_id"] = domain.NewStringScalar(tc.Child.Model)
	}
	if tc.Child.Instance != "" {
		event.Labels["model_inst_id"] = domain.NewStringScalar(tc.Child.Instance)
	}
	result := mustProcessAggregation(t, repo, processor, event)
	if len(result.Event.RelatedAlertIDs) != 1 {
		t.Fatal("child missing Alert")
	}
	current, err := repo.GetAlert(t.Context(), "tenant", result.Event.RelatedAlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	out := shieldBehaviorComparisonResult{Shielded: current.Alert.Shield.Active}
	if out.Shielded {
		if len(current.Alert.Shield.Bindings) != 1 || current.Alert.Admission.AdmittedAt != nil {
			t.Fatal("shield admission/relationship invalid")
		}
		if id := current.Alert.Shield.Bindings[0].MainAlertID; id != "" {
			var ok bool
			out.Parent, ok = aliases[id]
			if !ok {
				t.Fatal("unknown bound parent")
			}
		}
	}
	// 被屏蔽子不得输出动作；未屏蔽子正常准入，主夹具本身的创建不使用这些 hook。
	childActions := 0
	for _, call := range action.calls {
		if call.Alert.AlertID == current.Alert.AlertID {
			childActions++
		}
	}
	if out.Shielded && childActions != 0 {
		t.Fatal("shielded child emitted action")
	}
	if !out.Shielded && (current.Alert.Admission.AdmittedAt == nil || childActions != 1) {
		t.Fatal("unshielded child did not admit exactly once")
	}
	if mode == "cmdb_shield" && (relations.calls == 0 || relations.origin.ModelID != mainModel || relations.origin.InstanceID != mainInstance) {
		t.Fatal("CMDB child match did not use selected main identity", relations)
	}
	return out
}
