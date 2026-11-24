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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
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

func TestRedisMergeMetadataAndDueSummaryAreIncremental(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	r.Duration = 2 * time.Minute
	first := joinMerge(t, s, r)
	commitMerge(t, s, r, first)
	meta, found, err := s.ReadMergeWindowMeta(t.Context(), r.TenantID, first.WindowID)
	if err != nil || !found || len(meta.Members) != 0 || meta.CommittedCount != 1 || !reflect.DeepEqual(meta.CoveredGroups, []int{0}) {
		t.Fatal("metadata loaded members or lost summary", meta, err)
	}
	due, err := s.ListMergeDue(t.Context(), r.TenantID, r.At.Add(time.Second), 32)
	if err != nil || len(due) != 0 {
		t.Fatal("incomplete new window scheduled repeatedly", due, err)
	}
	other := r
	other.Member = domain.DependencyMain{AlertID: "alert-b", EventID: "event-b", EventSourceID: "source-b", Fingerprint: "fp-b", Severity: "warning"}
	other.Groups = []int{1}
	other.At = other.At.Add(time.Second)
	second := joinMerge(t, s, other)
	commitMerge(t, s, other, second)
	commitMerge(t, s, other, second)
	meta, _, err = s.ReadMergeWindowMeta(t.Context(), r.TenantID, first.WindowID)
	if err != nil || meta.CommittedCount != 2 || !reflect.DeepEqual(meta.CoveredGroups, []int{0, 1}) {
		t.Fatal("duplicate inflated summary", meta, err)
	}
	due, err = s.ListMergeDue(t.Context(), r.TenantID, other.At, 32)
	if err != nil || len(due) != 1 {
		t.Fatal("ready window not scheduled", due, err)
	}
	if _, frozen, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, meta.Revision, other.At, []MergeCandidate{{AlertID: r.Member.AlertID, Groups: []int{0}}}); err != nil || frozen {
		t.Fatal("invalid current set frozen", err)
	}
	due, err = s.ListMergeDue(t.Context(), r.TenantID, other.At.Add(time.Second), 32)
	if err != nil || len(due) != 0 {
		t.Fatal("failed readiness remained hot", due, err)
	}
	commitMerge(t, s, other, second)
	due, err = s.ListMergeDue(t.Context(), r.TenantID, other.At.Add(time.Second), 32)
	if err != nil || len(due) != 1 {
		t.Fatal("later related event did not wake window", due, err)
	}
	if err := s.client.HSet(t.Context(), s.mergeWindowKey(r.TenantID, first.WindowID), "m:unread", "invalid member").Err(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ReadMergeWindowMeta(t.Context(), r.TenantID, first.WindowID); err != nil || !found {
		t.Fatal("metadata read enumerated member payloads", err)
	}
	if _, _, err := s.ReadMergeWindow(t.Context(), r.TenantID, first.WindowID); !errors.Is(err, ErrState) {
		t.Fatal("full read should still reject damaged member", err)
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

func TestRedisMergeOutOfOrderProcessingTimeKeepsWindowAndFrozenBoundary(t *testing.T) {
	for _, cyclic := range []bool{false, true} {
		t.Run(fmt.Sprint("cyclic=", cyclic), func(t *testing.T) {
			s := redisClipStore(t)
			latest := mergeFixture()
			latest.Cyclic = cyclic
			latest.At = latest.At.Add(time.Second)
			first := joinMerge(t, s, latest)
			commitMerge(t, s, latest, first)
			earlier := latest
			earlier.At = latest.At.Add(-500 * time.Millisecond)
			earlier.Member.AlertID, earlier.Member.EventID = "alert-earlier", "event-earlier"
			earlier.Groups = []int{1}
			second := joinMerge(t, s, earlier)
			if second.WindowID != first.WindowID || second.StartedAt != first.StartedAt || second.Deadline != first.Deadline {
				t.Fatal("out-of-order request split or moved fixed window", first, second)
			}
			commitMerge(t, s, earlier, second)
			before := readMerge(t, s, latest, first)
			for _, member := range before.Members {
				if member.FirstAtMillis != first.StartedAt.UnixMilli() {
					t.Fatal("member was outside half-open window", member)
				}
			}
			if repeated := joinMerge(t, s, earlier); !reflect.DeepEqual(repeated, second) {
				t.Fatal("normalized request replay moved first result", second, repeated)
			}
			after := readMerge(t, s, latest, first)
			if after.Revision != before.Revision || len(after.Members) != 2 || after.DeadlineMillis != before.DeadlineMillis {
				t.Fatal("normalized replay changed membership or deadline", before, after)
			}
			at := latest.At.Add(time.Second)
			if cyclic {
				at = first.Deadline
			}
			selection := []MergeCandidate{{AlertID: latest.Member.AlertID, Groups: latest.Groups}, {AlertID: earlier.Member.AlertID, Groups: earlier.Groups}}
			frozen, ok, err := s.FreezeMergeWindow(t.Context(), latest.TenantID, first.WindowID, after.Revision, at, selection)
			if err != nil || !ok || frozen.Frozen.Outcome != "succeeded" {
				t.Fatal("normalized window failed to freeze", frozen, ok, err)
			}
			late := earlier
			late.Member.AlertID, late.Member.EventID = "alert-late", "event-late"
			if _, err := s.JoinMergeWindow(t.Context(), late); !errors.Is(err, policy.ErrUnavailable) {
				t.Fatal("older unregistered request reopened frozen window", err)
			}
			if current := readMerge(t, s, latest, first); current.Revision != frozen.Revision || len(current.Members) != 2 {
				t.Fatal("frozen membership changed after old request", current)
			}
			if repeated := joinMerge(t, s, earlier); !reflect.DeepEqual(repeated, second) {
				t.Fatal("frozen retry lost original normalized reference", second, repeated)
			}
			next := latest
			next.Member.AlertID, next.Member.EventID = "alert-next", "event-next"
			next.At = first.Deadline
			if boundary := joinMerge(t, s, next); boundary.WindowID == first.WindowID || !boundary.StartedAt.Equal(first.Deadline) || !boundary.Deadline.Equal(first.Deadline.Add(latest.Duration)) {
				t.Fatal("exact deadline no longer starts a new window", first, boundary)
			}
		})
	}
}

func TestRedisMerge255ConcurrentOlderRequestsShareExistingWindow(t *testing.T) {
	s := redisClipStore(t)
	latest := mergeFixture()
	latest.Cyclic = true
	latest.At = latest.At.Add(time.Second)
	first := joinMerge(t, s, latest)
	commitMerge(t, s, latest, first)
	type result struct {
		request MergeRequest
		wait    domain.MergeWait
		err     error
	}
	start := make(chan struct{})
	out := make(chan result, 255)
	var wg sync.WaitGroup
	for i := range 255 {
		wg.Go(func() {
			request := latest
			request.At = latest.At.Add(-time.Duration(i+1) * time.Millisecond)
			request.Member.AlertID, request.Member.EventID = fmt.Sprintf("alert-older-%03d", i), fmt.Sprintf("event-older-%03d", i)
			request.Groups = []int{i % 2}
			<-start
			wait, err := s.JoinMergeWindow(t.Context(), request)
			out <- result{request, wait, err}
		})
	}
	close(start)
	wg.Wait()
	close(out)
	for result := range out {
		if result.err != nil || result.wait.WindowID != first.WindowID || result.wait.StartedAt != first.StartedAt || result.wait.Deadline != first.Deadline {
			t.Fatal("concurrent older request lost its window", result.wait, result.err)
		}
		commitMerge(t, s, result.request, result.wait)
		if repeated := joinMerge(t, s, result.request); !reflect.DeepEqual(repeated, result.wait) {
			t.Fatal("concurrent normalized replay changed first reference", result.wait, repeated)
		}
	}
	window := readMerge(t, s, latest, first)
	if len(window.Members) != 256 || window.CommittedCount != 256 || window.StartedAtMillis != first.StartedAt.UnixMilli() || window.DeadlineMillis != first.Deadline.UnixMilli() {
		t.Fatal("concurrent reversed requests changed window boundary or lost members", len(window.Members), window.CommittedCount, window.StartedAtMillis, window.DeadlineMillis)
	}
	for _, member := range window.Members {
		if !member.Committed || member.FirstAtMillis != first.StartedAt.UnixMilli() {
			t.Fatal("concurrent member outside fixed window", member)
		}
	}
}

func TestRedisMergeByteBudgetDoesNotTruncateIntoSuccess(t *testing.T) {
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
	// 构造已接近总字节预算的资源计数，新的成员必须原子失败，不能截断已有集合。
	full := readMerge(t, s, r, first)
	full.Members = nil
	full.MemberBytes = mergeWindowMaxBytes
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.client.HSet(t.Context(), s.mergeWindowKey(r.TenantID, first.WindowID), "meta", raw).Err(); err != nil {
		t.Fatal(err)
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
