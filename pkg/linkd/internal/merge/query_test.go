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
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

func TestListDecisionsKeepsCompletedAndTenantPages(t *testing.T) {
	j, docs := newJournal(t)
	ids := []string{}
	for _, tenant := range []string{"tenant", "other"} {
		for _, window := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
			d := decisionFixture(t)
			d.TenantID = tenant
			d.Policy.TenantID = tenant
			d.WindowID = window
			d.ID, _ = domain.MergeDecisionID(tenant, window)
			d.Outcome = "failed"
			d.FrozenAt = d.Deadline
			s, err := j.Claim(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			s, err = j.AdvanceMembers(t.Context(), s, len(d.WaitMemberIDs), d.FrozenAt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = j.Complete(t.Context(), s, nil, d.FrozenAt); err != nil {
				t.Fatal(err)
			}
			if tenant == "tenant" {
				ids = append(ids, d.ID)
			}
		}
	}
	slices.Sort(ids)
	page, err := j.ListDecisions(t.Context(), "tenant", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[0] || page.Items[0].Progress.Phase != "completed" || page.Next != ids[0] {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page, err = j.ListDecisions(t.Context(), "tenant", page.Next, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[1] {
		t.Fatal(page, err)
	}
	for _, q := range []struct {
		tenant, after string
		limit         int
	}{{"", "", 1}, {"tenant", "bad", 1}, {"tenant", "", 17}} {
		if _, err := j.ListDecisions(t.Context(), q.tenant, q.after, q.limit); !errors.Is(err, policy.ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := j.ListDecisions(ctx, "tenant", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// 即使存储索引错误返回别租户的合法记录，也不能把它作为本租户结果输出。
	key, _ := decisionKey("tenant", ids[0])
	other, _ := decisionKey("other", func() string { v, _ := domain.MergeDecisionID("other", strings.Repeat("a", 64)); return v }())
	docs.data["merge_decisions/"+key] = slices.Clone(docs.data["merge_decisions/"+other])
	if _, err := j.ListDecisions(t.Context(), "tenant", "", 1); !errors.Is(err, policy.ErrAccess) {
		t.Fatal(err)
	}
	docs.data["merge_decisions/"+key] = json.RawMessage(`{}`)
	if _, err := j.ListDecisions(t.Context(), "tenant", "", 1); err == nil {
		t.Fatal("malformed record accepted")
	}
}
