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
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func TestSystemClosePersistsExactOperationIdentity(t *testing.T) {
	for _, source := range []string{"auto_policy", "work_order", "self_heal", "strategy_change", "system"} {
		t.Run(source, func(t *testing.T) {
			repo := memory.New()
			p := newTestProcessor(t, repo, &recordingHook{})
			event := testEvent("opening", "warning")
			result := persistAndProcess(t, repo, p, event)
			command := CloseAlertCommand{BKTenantID: event.BKTenantID, AlertID: result.AlertIDs[0], OperationID: "stable-op", OperatorKind: domain.OperatorKindSystem, OperatorID: "kac-worker", OperationSource: source, Reason: "completed", EffectiveAt: event.ReceivedAt.Add(time.Hour)}
			closed, err := p.CloseAlert(t.Context(), command)
			if err != nil || closed.Alert.Status != domain.AlertStatusClosed || closed.Alert.EndType != domain.AlertEndTypeSystem || closed.Alert.EndOperation == nil || closed.Alert.EndOperation.Source != source {
				t.Fatal("system close identity", closed, err)
			}
			repeat, err := p.CloseAlert(t.Context(), command)
			if err != nil || !repeat.AlreadyClosed || repeat.Alert.Revision != closed.Alert.Revision {
				t.Fatal("retry changed terminal", err)
			}
			for _, change := range []func(*CloseAlertCommand){func(c *CloseAlertCommand) { c.OperationID = "other" }, func(c *CloseAlertCommand) { c.OperatorID = "other" }, func(c *CloseAlertCommand) { c.OperationSource = "manual" }, func(c *CloseAlertCommand) { c.Reason = "different" }} {
				other := command
				change(&other)
				if _, err = p.CloseAlert(t.Context(), other); err == nil {
					t.Fatal("conflicting terminal operation accepted")
				}
			}
			canceled := t.Context() // 终态冲突仍保持明确错误类型。
			command.OperationID = "another"
			if _, err = p.CloseAlert(canceled, command); !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatal(err)
			}
		})
	}
}
