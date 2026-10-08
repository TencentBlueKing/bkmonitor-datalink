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
	"context"
	"reflect"
	"testing"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store/storetest"
)

type durableRelationActions struct {
	items map[string]actiondelivery.Request
}

func (r *durableRelationActions) RecordAction(ctx context.Context, a domain.Alert, intent domain.AlertActionIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := intent.Validate(a); err != nil {
		return err
	}
	for id := range intent.Targets {
		q, err := actiondelivery.BuildRequest(a, id, actiondelivery.Cause{Type: intent.CauseType, ID: intent.CauseID})
		if err != nil {
			return err
		}
		if r.items == nil {
			r.items = map[string]actiondelivery.Request{}
		}
		r.items[q.ActionID] = q
	}
	return nil
}

func TestDurableMergeActionsParentAdmissionRecoveryAndManualUnlink(t *testing.T) {
	for _, ending := range []string{"manual parent close", "all children recovered"} {
		t.Run(ending, func(t *testing.T) {
			recorder := &durableRelationActions{}
			f := newConfiguredRelationFixture(t, func(a *domain.Alert) {
				a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {SourceVersion: a.EventSourceVersion, RequiredRevision: 1, ActionEnabled: true}}
			}, lifecycle.WithActionRecorder(recorder), lifecycle.WithInitialProjectionTargets(map[string]bool{"kac": true}))
			if len(recorder.items) != 0 {
				t.Fatal("unready parent already enqueued action")
			}
			finishLinks(t, f)
			if len(recorder.items) != 1 {
				t.Fatal("ready parent did not enqueue one firing", len(recorder.items))
			}
			child, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
			if err != nil {
				t.Fatal(err)
			}
			if ending == "manual parent close" {
				closeRelationParent(t, f)
				finishEnding(t, f)
				if len(recorder.items) != 2 {
					t.Fatal("unlink enqueued child action", len(recorder.items))
				}
				released, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
				if err != nil || released.Alert.ActionPending != nil || released.Alert.Status != domain.AlertStatusActive || !reflect.DeepEqual(released.Alert.Admission, child.Alert.Admission) {
					t.Fatal("unlink changed child admission", err)
				}
				e := storetest.Event(f.decision.TenantID, "next-trigger", child.Alert.Fingerprint, child.Alert.Severity)
				e.EventSourceID = child.Alert.EventSourceID
				row, err := f.repo.CreateEvent(t.Context(), e)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.processor.ProcessEvent(t.Context(), row.StoredEvent); err != nil {
					t.Fatal(err)
				}
				if len(recorder.items) != 3 {
					t.Fatal("next child event did not enqueue original lifecycle action")
				}
			} else {
				endChild(t, f, "a")
				endChild(t, f, "b")
				finishEnding(t, f)
				if len(recorder.items) != 2 {
					t.Fatal("parent recovery action missing", len(recorder.items))
				}
				recovered := false
				for _, q := range recorder.items {
					if q.Action == "resolved" && q.Cause.Type == "system_operation" {
						recovered = true
					}
				}
				if !recovered {
					t.Fatal("parent recovery lacked stable system cause")
				}
			}
		})
	}
}
