// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	mergeflow "linkd/internal/merge"
	"linkd/internal/policy"
)

func runMergeRetryContract(t *testing.T, s *Store, reopen func() *Store, d mergeflow.Decision) {
	t.Helper()
	j, e := mergeflow.NewJournal(s)
	if e != nil {
		t.Fatal(e)
	}
	point, e := j.ReadControlPoint(t.Context(), d.TenantID, "decisions", d.ID)
	if e != nil {
		t.Fatal(e)
	}
	command := mergeflow.RetryCommand{TenantID: d.TenantID, Kind: "decisions", TargetID: d.ID, ExpectedToken: point.Token, OperationID: "retry-contract", OperatorID: "tester", Reason: "验证持久原版本"}
	first, e := j.EnqueueRetry(t.Context(), command, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	scope := base64.RawURLEncoding.EncodeToString([]byte(d.TenantID)) + ":"
	if n, e := s.CountMergeRequests(t.Context(), scope, 1024); e != nil || n != 1 {
		t.Fatal("pending count not visible", n, e)
	}
	restored, e := mergeflow.NewJournal(reopen())
	if e != nil {
		t.Fatal(e)
	}
	replay, e := restored.EnqueueRetry(t.Context(), command, time.Now())
	if e != nil || !reflect.DeepEqual(replay, first) {
		t.Fatal("request changed after reopen", e)
	}
	changed := command
	changed.Reason = "changed"
	if _, e := restored.EnqueueRetry(t.Context(), changed, time.Now()); !errors.Is(e, policy.ErrConflict) {
		t.Fatal(e)
	}
	started, e := restored.StartRetry(t.Context(), replay, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	again, e := restored.StartRetry(t.Context(), started, time.Now())
	if e != nil || !again.Request.PreviousUnconfirmed || !again.Request.StartedAt.Equal(*started.Request.StartedAt) {
		t.Fatal("uncertainty lost", e)
	}
	result := mergeflow.RetryResult{CheckedAt: time.Now(), Outcome: "unchanged", Reason: "no_progress", StepAttempted: true, Before: &point, After: &point}
	finished, e := restored.FinishRetry(t.Context(), again, result)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := restored.FinishRetry(t.Context(), again, result); !errors.Is(e, policy.ErrConflict) {
		t.Fatal("stale result CAS accepted", e)
	}
	found, e := restored.FindRetry(t.Context(), command)
	if e != nil || !reflect.DeepEqual(found, finished) {
		t.Fatal("final retry changed", e)
	}
	refreshMergeFixture(t, s, "merge_requests")
	work, e := restored.RetryWork(t.Context(), "", 16)
	if e != nil || len(work.Items) != 0 {
		t.Fatal("completed request remains work", e)
	}
	page, e := restored.ListRetries(t.Context(), d.TenantID, "decisions", d.ID, "", 1)
	if e != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("history missing", e)
	}
	other, e := restored.ListRetries(t.Context(), "other-tenant", "decisions", d.ID, "", 1)
	if e != nil || len(other.Items) != 0 {
		t.Fatal("history tenant leak", e)
	}
	if _, e := restored.ListRetries(t.Context(), "other-tenant", "decisions", d.ID, page.Next, 1); !errors.Is(e, policy.ErrInvalid) {
		t.Fatal("foreign cursor accepted", e)
	}
	after, e := j.ReadControlPoint(t.Context(), d.TenantID, "decisions", d.ID)
	if e != nil || !reflect.DeepEqual(after, point) {
		t.Fatal("request storage changed business progress", e)
	}
}
