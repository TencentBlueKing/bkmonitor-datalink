// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store/memory"
)

func TestExplicitCustomContentHasPriorityOverAutomaticDescription(t *testing.T) {
	for _, text := range []string{"custom", "same as source", ""} {
		for _, status := range []string{"succeeded", "partial", "failed", "skipped"} {
			t.Run(text+"/"+status, func(t *testing.T) {
				processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
				called := false
				processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
					called = true
					return "automatic", nil
				})
				value, _ := json.Marshal(text)
				processors := json.RawMessage(`[{"fields":{"status":"` + status + `","patches":[{"path":"$.content","value":` + string(value) + `}]}}]`)
				event := testEvent("custom-content", "warning")
				event.Content = "same as source"
				opening := domain.Alert{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
					EventSourceVersion: event.EventSourceVersion, TriggerEventID: event.EventID,
					Fingerprint: event.Fingerprint, Severity: "warning", Enrich: domain.JSONObject{"processors": processors}}
				content, err := processor.buildAlertContent(t.Context(), event, event.Evaluations[0], opening)
				if err != nil {
					t.Fatal(err)
				}
				if status == "failed" || status == "skipped" {
					if content != "automatic" || !called {
						t.Fatal("uncommitted output overrode automatic content")
					}
				} else if content != text || called {
					t.Fatalf("explicit content lost: %q, builder called=%v", content, called)
				}
			})
		}
	}
}
