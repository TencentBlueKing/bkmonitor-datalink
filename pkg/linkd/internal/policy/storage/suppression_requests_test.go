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
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/policy"
	"linkd/internal/suppressioncheck"
)

func runSuppressionRequestContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	j, err := suppressioncheck.NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	c := suppressioncheck.Command{TenantID: "suppression-check", Kind: "aggregation", WindowID: strings.Repeat("b", 64), ExpectedEpoch: "first", ExpectedOwner: "original-owner", OperationID: "original", OperatorID: "tester", Reason: "复核原窗口"}
	first, err := j.Enqueue(t.Context(), c, time.Now())
	if err != nil || first.Request.State != "pending" {
		t.Fatal(first, err)
	}
	prefix := base64.RawURLEncoding.EncodeToString([]byte(c.TenantID)) + ":"
	for _, limit := range []int{1, 1024} {
		if count, err := s.CountSuppressionCheckRequests(t.Context(), prefix, limit); err != nil || count != 1 {
			t.Fatal("pending work not visible", count, err)
		}
	}
	restored, err := suppressioncheck.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restored.Enqueue(t.Context(), c, time.Now())
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("reopen changed initial command", err)
	}
	changed := c
	changed.ExpectedEpoch = "later"
	if _, err := restored.Enqueue(t.Context(), changed, time.Now()); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("same operation changed epoch", err)
	}
	started, err := restored.Start(t.Context(), replay, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err := restored.Start(t.Context(), started, time.Now())
	if err != nil || !again.Request.PreviousUnconfirmed || !again.Request.StartedAt.Equal(*started.Request.StartedAt) {
		t.Fatal("resume lost uncertainty", again, err)
	}
	finished, err := restored.Finish(t.Context(), again, suppressioncheck.Check{CheckedAt: time.Now(), Outcome: "absent", Reason: "window_missing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Finish(t.Context(), again, suppressioncheck.Check{CheckedAt: time.Now(), Outcome: "absent", Reason: "window_missing"}); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale final CAS accepted", err)
	}
	found, err := restored.Find(t.Context(), c)
	if err != nil || !reflect.DeepEqual(found.Request, finished.Request) {
		t.Fatal("final replay mutated", err)
	}
	if _, err := restored.Get(t.Context(), "other", c.Kind, c.WindowID, first.Request.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("tenant leak", err)
	}
	if s.transport != nil {
		if code, _, err := s.request(t.Context(), http.MethodPost, "/"+s.table("suppression_requests")+"/_refresh", nil); err != nil || code != 200 {
			t.Fatal("refresh owned control fixture", code, err)
		}
	}
	if count, err := s.CountSuppressionCheckRequests(t.Context(), prefix, 1024); err != nil || count != 0 {
		t.Fatal("completed work retained", count, err)
	}
	if work, err := restored.Work(t.Context(), "", 16); err != nil || len(work.Items) != 0 {
		t.Fatal(work, err)
	}
	history, err := restored.List(t.Context(), c.TenantID, c.Kind, c.WindowID, "", 1)
	if err != nil || len(history.Items) != 1 || history.Items[0].State != "completed" || history.Next == "" {
		t.Fatal(history, err)
	}
	if end, err := restored.List(t.Context(), c.TenantID, c.Kind, c.WindowID, history.Next, 1); err != nil || len(end.Items) != 0 {
		t.Fatal(end, err)
	}
}
