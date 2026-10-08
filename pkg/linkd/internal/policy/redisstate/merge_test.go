// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
)

func mergeFixture() MergeRequest {
	return MergeRequest{TenantID: "tenant", Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("a", 64)}, GroupKey: strings.Repeat("b", 64), Member: domain.DependencyMain{AlertID: "alert-a", EventID: "event-a", EventSourceID: "source-a", Fingerprint: "fp-a", Severity: "warning"}, Groups: []int{0}, GroupCount: 2, At: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Duration: time.Minute}
}

func joinMerge(t *testing.T, s *Store, r MergeRequest) domain.MergeWait {
	t.Helper()
	w, err := s.JoinMergeWindow(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func commitMerge(t *testing.T, s *Store, r MergeRequest, w domain.MergeWait) {
	t.Helper()
	if ok, err := s.CommitMergeMember(t.Context(), r.TenantID, r.Member.AlertID, w); err != nil || !ok {
		t.Fatal("commit merge", err)
	}
}

func readMerge(t *testing.T, s *Store, r MergeRequest, w domain.MergeWait) MergeWindow {
	t.Helper()
	v, found, err := s.ReadMergeWindow(t.Context(), r.TenantID, w.WindowID)
	if err != nil || !found {
		t.Fatal("read merge", err)
	}
	return v
}

func TestRedisMergeRequiresRealDistinctMembersAndFreezesOnce(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	r.Groups = []int{0, 1}
	first := joinMerge(t, s, r)
	v := readMerge(t, s, r, first)
	selection := []MergeCandidate{{AlertID: r.Member.AlertID, Groups: r.Groups}}
	if _, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, r.At, selection); err != nil || ok {
		t.Fatal("uncommitted member counted", err)
	}
	commitMerge(t, s, r, first)
	v = readMerge(t, s, r, first)
	if _, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, r.At, selection); err != nil || ok {
		t.Fatal("one member satisfied all groups alone", err)
	}
	other := r
	other.Member = domain.DependencyMain{AlertID: "alert-b", EventID: "event-b", EventSourceID: "source-b", Fingerprint: "fp-b", Severity: "warning"}
	other.At = other.At.Add(time.Second)
	second := joinMerge(t, s, other)
	if second.WindowID != first.WindowID || second.Deadline != first.Deadline {
		t.Fatal("source split or extended window")
	}
	commitMerge(t, s, other, second)
	selection = append(selection, MergeCandidate{AlertID: other.Member.AlertID, Groups: other.Groups})
	if _, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, other.At, selection); err != nil || ok {
		t.Fatal("stale revision froze newer membership", err)
	}
	v = readMerge(t, s, r, first)
	frozen, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, other.At, selection)
	if err != nil || !ok || frozen.Frozen.Outcome != "succeeded" || len(frozen.Frozen.MemberIDs) != 2 {
		t.Fatal("early success failed", err)
	}
	retry, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, other.At.Add(time.Hour), nil)
	if err != nil || !ok || !reflect.DeepEqual(retry.Frozen, frozen.Frozen) {
		t.Fatal("frozen decision changed", err)
	}
	third := other
	third.Member.AlertID = "alert-c"
	third.Member.EventID = "event-c"
	third.At = other.At.Add(time.Second)
	next := joinMerge(t, s, third)
	if next.WindowID == first.WindowID {
		t.Fatal("new member joined frozen window")
	}
	if err := s.FinishMergeWindow(t.Context(), r.TenantID, first.WindowID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ReadMergeWindow(t.Context(), r.TenantID, next.WindowID); err != nil || !found {
		t.Fatal("old finish deleted new window", err)
	}
	// 原操作在窗口冻结后重投，仍返回首次引用，正式 runtime 会识别已裁决并避免重新挂等待。
	if replay := joinMerge(t, s, r); !reflect.DeepEqual(replay, first) {
		t.Fatal("replay moved first result")
	}
}

func TestRedisMergeCyclicDeadlineAndHalfOpenWindow(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	r.Cyclic = true
	first := joinMerge(t, s, r)
	commitMerge(t, s, r, first)
	other := r
	other.Member.AlertID = "alert-b"
	other.Member.EventID = "event-b"
	other.Groups = []int{1}
	other.At = first.Deadline.Add(-time.Millisecond)
	second := joinMerge(t, s, other)
	commitMerge(t, s, other, second)
	if second.WindowID != first.WindowID {
		t.Fatal("last millisecond excluded")
	}
	v := readMerge(t, s, r, first)
	c := []MergeCandidate{{AlertID: r.Member.AlertID, Groups: r.Groups}, {AlertID: other.Member.AlertID, Groups: other.Groups}}
	if _, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, other.At, c); err != nil || ok {
		t.Fatal("cycle succeeded early", err)
	}
	next := other
	next.Member.AlertID = "alert-c"
	next.Member.EventID = "event-c"
	next.At = first.Deadline
	third := joinMerge(t, s, next)
	if third.WindowID == first.WindowID || !third.StartedAt.Equal(first.Deadline) {
		t.Fatal("exact deadline stayed in old window")
	}
	frozen, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, v.Revision, first.Deadline, c)
	if err != nil || !ok || frozen.Frozen.Outcome != "succeeded" {
		t.Fatal("cycle did not succeed at deadline", err)
	}
	if _, exists, err := s.ReadMergeWindow(t.Context(), "other-tenant", first.WindowID); err != nil || exists {
		t.Fatal("cross tenant window", err)
	}
}

func TestRedisMergeIncompleteWindowFailsAndLostCacheCannotBeCommitted(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	w := joinMerge(t, s, r)
	commitMerge(t, s, r, w)
	v := readMerge(t, s, r, w)
	failed, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, w.WindowID, v.Revision, w.Deadline, nil)
	if err != nil || !ok || failed.Frozen.Outcome != "failed" {
		t.Fatal("expired incomplete window did not fail", err)
	}
	due, err := s.ListMergeDue(t.Context(), r.TenantID, w.Deadline, 32)
	if err != nil || len(due) != 1 || due[0] != w.WindowID {
		t.Fatal("missing due hint", err)
	}
	if err := s.FinishMergeWindow(t.Context(), r.TenantID, w.WindowID); err != nil {
		t.Fatal(err)
	}
	due, err = s.ListMergeDue(t.Context(), r.TenantID, w.Deadline, 32)
	if err != nil || len(due) != 0 {
		t.Fatal("completed hint retained", err)
	}
	if err := s.client.Del(t.Context(), s.mergeWindowKey(r.TenantID, w.WindowID)).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CommitMergeMember(t.Context(), r.TenantID, r.Member.AlertID, w); err != nil || ok {
		t.Fatal("commit recreated lost window", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.ReadMergeWindow(ctx, r.TenantID, w.WindowID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel swallowed", err)
	}
}

func TestRedisMergeConcurrentMembersShareOneWindow(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	type result struct {
		r   MergeRequest
		w   domain.MergeWait
		err error
	}
	out := make(chan result, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			v := r
			v.Member.AlertID = fmt.Sprintf("alert-%02d", i)
			v.Member.EventID = fmt.Sprintf("event-%02d", i)
			v.Member.EventSourceID = fmt.Sprintf("source-%02d", i)
			v.Groups = []int{i % 2}
			w, err := s.JoinMergeWindow(t.Context(), v)
			out <- result{v, w, err}
		})
	}
	wg.Wait()
	close(out)
	id := ""
	var first domain.MergeWait
	for v := range out {
		if v.err != nil {
			t.Fatal(v.err)
		}
		if id != "" && id != v.w.WindowID {
			t.Fatal("concurrent windows split")
		}
		id = v.w.WindowID
		first = v.w
		commitMerge(t, s, v.r, v.w)
	}
	v := readMerge(t, s, r, first)
	if len(v.Members) != 32 {
		t.Fatal("members overwritten", len(v.Members))
	}
	repeat := r
	repeat.Member = v.Members[0].Main
	repeat.Member.EventID = "repeat-event"
	repeat.Groups = []int{0, 1}
	repeat.At = repeat.At.Add(30 * time.Second)
	again := joinMerge(t, s, repeat)
	if again.MemberEventID != v.Members[0].Main.EventID || again.Deadline != first.Deadline {
		t.Fatal("duplicate alert moved first match")
	}
	after := readMerge(t, s, r, first)
	if len(after.Members) != 32 || after.Revision != v.Revision {
		t.Fatal("duplicate alert increased member count or revision")
	}
}

func TestRedisMergeMemberBudgetDoesNotTruncateIntoSuccess(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	var first domain.MergeWait
	for i := range 256 {
		v := r
		v.Member.AlertID = fmt.Sprintf("alert-%03d", i)
		v.Member.EventID = fmt.Sprintf("event-%03d", i)
		w := joinMerge(t, s, v)
		if i == 0 {
			first = w
		}
	}
	overflow := r
	overflow.Member.AlertID = "overflow"
	overflow.Member.EventID = "overflow"
	if _, err := s.JoinMergeWindow(t.Context(), overflow); !errors.Is(err, ErrBudget) {
		t.Fatal("overflow was not explicit", err)
	}
	w := readMerge(t, s, r, first)
	if len(w.Members) != 256 {
		t.Fatal("overflow changed existing membership")
	}
	if _, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, w.Revision, r.At, []MergeCandidate{{AlertID: r.Member.AlertID, Groups: []int{0, 1}}}); err != nil || ok {
		t.Fatal("reserved member counted after budget error", err)
	}
}
