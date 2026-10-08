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
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	mergeflow "linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store/storetest"
)

func runMergeJournalContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	j, err := mergeflow.NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"name":"merge","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":[{"expression":"A","A":{"condition":"term","target_key":"level","target_value":"warning"}}],"merge_cycle":60,"is_cycle_merge":false,"aggregate_fields":[],"alarm_tags":[9,2],"new_alarm_config":[{"key":"name","value":"joint ${alarm_num}"},{"key":"level","value":"warning"},{"key":"content","value":"${cw_merged_meta_info}"}]}`)
	c, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	id, err := domain.MergeDecisionID("tenant-merge", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	d := mergeflow.Decision{ID: id, TenantID: "tenant-merge", WindowID: strings.Repeat("b", 64), GroupKey: strings.Repeat("c", 64), Policy: policy.Release{Scope: policy.Scope{TenantID: "tenant-merge", Kind: policy.Merge}, ID: "merge", Version: 1, Spec: c.Canonical, Compiled: c.Summary}, FrozenAt: at, StartedAt: at, Deadline: at.Add(time.Minute), Outcome: "succeeded", MemberIDs: []string{"a", "b"}, WaitMemberIDs: []string{"a", "b"}}
	_, err = j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range d.MemberIDs {
		alert := storetest.Alert(d.TenantID, id, "event-"+id, "fp-"+id, "warning")
		alert.ExtraData["meta_info"] = json.RawMessage(`{"z":false,"a":9007199254740993}`)
		if _, err := j.Capture(t.Context(), d.TenantID, d.ID, alert); err != nil {
			t.Fatal(err)
		}
	}
	source := config.EventSource{EventSourceID: domain.BuiltinMergeEventSourceID, Version: 1, Enabled: true, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeInternalMerge}}.WithDefaults()
	release := eventsource.Release{ID: source.EventSourceID, Version: 1, Spec: source}
	prepared, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, release, runtimeconfig.Snapshot{Severity: config.DefaultSeverityConfig()}, at)
	if err != nil {
		t.Fatal(err)
	}
	runMergeRetryContract(t, s, reopen, prepared.Decision)
	event := prepared.Decision.Progress.ParentEvent
	if event.Content != `{"a":9007199254740993,"z":false}` || !reflect.DeepEqual(event.MergeOrigin.AlarmTags, []int64{2, 9}) {
		t.Fatal("frozen snapshot rendering lost JSON precision or policy tags")
	}
	restarted, err := mergeflow.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.Get(t.Context(), d.TenantID, d.ID)
	if err != nil || !reflect.DeepEqual(resumed.Decision, prepared.Decision) {
		t.Fatal("prepared merge did not survive reopen", err)
	}
	refreshMergeFixture(t, s, "merge_decisions")
	decisionPage, err := restarted.ListDecisions(t.Context(), d.TenantID, "", 1)
	if err != nil || len(decisionPage.Items) != 1 || decisionPage.Items[0].ID != d.ID || decisionPage.Next != d.ID {
		t.Fatal("tenant decision page lost stored record", err)
	}
	otherPage, err := restarted.ListDecisions(t.Context(), "other-tenant", "", 1)
	if err != nil || len(otherPage.Items) != 0 {
		t.Fatal("decision page leaked tenant", err)
	}
	members, err := restarted.Members(t.Context(), resumed.Decision)
	if err != nil || len(members) != 2 {
		t.Fatal("snapshot rows missing after reopen", err)
	}
	pending, err := restarted.MarkEnqueued(t.Context(), resumed, at)
	if err != nil || pending.Decision.Progress.Phase != "waiting_parent" {
		t.Fatal(err)
	}
	if _, err := j.MarkEnqueued(t.Context(), prepared, at); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("old instance overwrote merge progress", err)
	}
	if _, err := restarted.Get(t.Context(), "other-tenant", d.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("merge tenant isolation", err)
	}
	refreshMergeFixture(t, s, "merge_decisions")
	page, err := restarted.ListWork(t.Context(), "", 16)
	if err != nil || len(page.Decisions) != 1 || page.Decisions[0].ID != d.ID {
		t.Fatal("pending merge not discoverable", err)
	}
}
