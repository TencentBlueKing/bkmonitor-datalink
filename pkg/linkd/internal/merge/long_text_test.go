// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/policy"
	"linkd/internal/runtimeconfig"
)

func TestLongDisplayTextSurvivesMergeStorageProjectionAndRetry(t *testing.T) {
	for _, text := range []string{strings.Repeat("a", 500), strings.Repeat("告警", 300), strings.Repeat("🚨", 2000)} {
		f := newExecutorFixture(t, func(spec *policy.MergeSpec) {
			spec.Template[0].Value = text + strings.Repeat("extra", 1000)
			spec.Template = append(spec.Template, policy.TemplateField{Key: "object", Value: text + strings.Repeat("extra", 1000)})
		})
		expected := string([]rune(text + strings.Repeat("extra", 1000))[:2000])
		startExecutor(t, f)
		done := driveExecutor(t, f)
		// 窗口清理失败后重试，不能截断标题或重复生成主告警。
		f.state.finishErr = errors.New("retry cleanup")
		if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second)); err == nil {
			t.Fatal("cleanup failure hidden")
		}
		if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
		if err != nil || parent.Alert.Title != expected || parent.Alert.SubjectName != expected || len(f.action.inputs) != 1 {
			t.Fatal("parent display text lost or duplicated", err)
		}
		relation, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil || relation.Relation.ParentTitle != expected || relation.Relation.State != "ready" {
			t.Fatal("stored relation title lost", err)
		}
		levels := runtimeconfig.Snapshot{Severity: config.DefaultSeverityConfig()}
		raw, err := kachook.CompatibilityPayload(parent.Alert, levels.KACLevel)
		if err != nil {
			t.Fatal(err)
		}
		var projected map[string]any
		if json.Unmarshal(raw, &projected) != nil || projected["name"] != expected || projected["object"] != expected {
			t.Fatal("KAC projection truncated display text")
		}
		relation.Relation.ParentTitle = strings.Repeat("🚨", 2000)
		if err := relation.Relation.Validate(); err != nil {
			t.Fatal("2000 character parent title rejected", err)
		}
		relation.Relation.ParentTitle += "x"
		if err := relation.Relation.Validate(); err == nil {
			t.Fatal("oversized parent title accepted")
		}
	}
}
