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
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

func runSuppressionCleanupContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	j, err := suppressioncleanup.NewJournal(s, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	cause := suppressioncleanup.Cause{TenantID: "cleanup", SourceID: "source", Fingerprint: "fp", Trigger: "alert_terminal", AlertID: "alert", Revision: 2, Status: domain.AlertStatusRecovered}
	if err := j.Run(t.Context(), cause, func(context.Context) (suppressioncleanup.Result, error) {
		return suppressioncleanup.Result{Clip: suppressioncleanup.Confirmed([]suppressioncleanup.Window{{ID: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Epoch: "first"}}), Aggregation: suppressioncleanup.Outcome{State: "unavailable"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	first, _, err := j.Get(t.Context(), cause.TenantID, cause.ID())
	if err != nil || first.Result == nil || first.Result.Aggregation.Removed != nil {
		t.Fatal(first, err)
	}
	restored, err := suppressioncleanup.NewJournal(reopen(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Run(t.Context(), cause, func(context.Context) (suppressioncleanup.Result, error) {
		t.Fatal("reopen replayed Redis operation")
		return suppressioncleanup.Result{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := restored.Get(t.Context(), cause.TenantID, cause.ID())
	if err != nil || !reflect.DeepEqual(first, got) {
		t.Fatal("reopen changed result", err)
	}
	if _, _, err := restored.Get(t.Context(), "other", cause.ID()); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("tenant leaked", err)
	}
	if s.transport != nil {
		if code, _, err := s.request(t.Context(), http.MethodPost, "/"+s.table("suppression_cleanups")+"/_refresh", nil); err != nil || code != 200 {
			t.Fatal("owned cleanup refresh", code, err)
		}
	}
	page, err := restored.List(t.Context(), cause.TenantID, "", 1)
	if err != nil || len(page.Items) != 1 || page.Next != cause.ID() {
		t.Fatal("history missing", page, err)
	}
	end, err := restored.List(t.Context(), cause.TenantID, page.Next, 1)
	if err != nil || len(end.Items) != 0 || end.Next != "" {
		t.Fatal("history cursor", end, err)
	}
}
