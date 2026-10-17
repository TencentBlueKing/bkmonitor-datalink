// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRedisMergeLargeWindowsUseByteBudget(t *testing.T) {
	for _, count := range []int{5000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s := redisClipStore(t)
			r := mergeFixture()
			r.Cyclic = true
			request := func(i int) MergeRequest {
				v := r
				v.Member.AlertID = fmt.Sprintf("%s-%05d", strings.Repeat("m", 96), i)
				v.Member.EventID, v.Member.Fingerprint = fmt.Sprintf("event-%05d", i), fmt.Sprintf("fp-%05d", i)
				v.Groups = []int{i % 2}
				return v
			}
			firstRequest := request(0)
			first := joinMerge(t, s, firstRequest)
			commitMerge(t, s, firstRequest, first)
			jobs := make(chan int, count-1)
			for i := 1; i < count; i++ {
				jobs <- i
			}
			close(jobs)
			var workers sync.WaitGroup
			for range 32 {
				workers.Go(func() {
					for i := range jobs {
						v := request(i)
						wait, err := s.JoinMergeWindow(t.Context(), v)
						if err != nil || wait.WindowID != first.WindowID || wait.Deadline != first.Deadline {
							t.Error("large window lost a member", i, err)
							continue
						}
						if ok, err := s.CommitMergeMember(t.Context(), v.TenantID, v.Member.AlertID, wait); err != nil || !ok {
							t.Error("large window lost committed member", i, err)
						}
					}
				})
			}
			workers.Wait()
			if t.Failed() {
				return
			}
			window := readMerge(t, s, r, first)
			if len(window.Members) != count || window.CommittedCount != count || window.MemberBytes <= 0 {
				t.Fatal("large window was truncated", len(window.Members), window.CommittedCount, window.MemberBytes)
			}
			selection := make([]MergeCandidate, count)
			for i, member := range window.Members {
				selection[i] = MergeCandidate{AlertID: member.Main.AlertID, Groups: member.Groups}
			}
			frozen, ok, err := s.FreezeMergeWindow(t.Context(), r.TenantID, first.WindowID, window.Revision, first.Deadline, selection)
			if err != nil || !ok || len(frozen.Frozen.MemberIDs) != count {
				t.Fatal("large full member set did not freeze", ok, err)
			}
			raw, err := s.client.HGet(t.Context(), s.mergeWindowKey(r.TenantID, first.WindowID), "meta").Bytes()
			if err != nil || len(raw) <= 256<<10 || len(raw) > mergeMetaMaxBytes || int64(len(raw))+frozen.MemberBytes > mergeWindowMaxBytes {
				t.Fatal("metadata still capped at old 256KiB or lost byte budget", len(raw), frozen.MemberBytes, err)
			}
			if repeated := joinMerge(t, s, firstRequest); !reflect.DeepEqual(repeated, first) {
				t.Fatal("large frozen window replay changed reference")
			}
			if err := s.FinishMergeWindow(t.Context(), r.TenantID, first.WindowID); err != nil {
				t.Fatal("large frozen metadata blocked cleanup", err)
			}
			t.Logf("members=%d member_bytes=%d frozen_meta_bytes=%d", count, frozen.MemberBytes, len(raw))
		})
	}
}

func TestRedisMergeLegacyWindowCountsBytesOnceAndReplaysDoNotInflate(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	first := joinMerge(t, s, r)
	key := s.mergeWindowKey(r.TenantID, first.WindowID)
	raw, err := s.client.HGet(t.Context(), key, "meta").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "member_bytes")
	raw, _ = json.Marshal(old)
	if err := s.client.HSet(t.Context(), key, "meta", raw).Err(); err != nil {
		t.Fatal(err)
	}
	other := r
	other.Member.AlertID, other.Member.EventID = "other", "other-event"
	second := joinMerge(t, s, other)
	commitMerge(t, s, other, second)
	values, err := s.client.HGetAll(t.Context(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	bytes := 0
	for field, value := range values {
		if field != "meta" {
			bytes += len(value)
		}
	}
	before := readMerge(t, s, r, first)
	if before.MemberBytes != int64(bytes) {
		t.Fatal("lazy byte count differs from actual serialized members", before.MemberBytes, bytes)
	}
	for range 3 {
		joinMerge(t, s, other)
		commitMerge(t, s, other, second)
	}
	after := readMerge(t, s, r, first)
	if after.MemberBytes != before.MemberBytes || after.Revision != before.Revision || after.CommittedCount != before.CommittedCount {
		t.Fatal("replay inflated byte/resource count", before.MemberBytes, after.MemberBytes)
	}
}
