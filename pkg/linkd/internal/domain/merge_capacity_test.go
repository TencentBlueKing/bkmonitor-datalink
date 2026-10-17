// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package domain_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func largeMergeRelation(count int) domain.MergeRelation {
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r := domain.MergeRelation{ID: strings.Repeat("a", 64), TenantID: "tenant", WindowID: strings.Repeat("b", 64), GroupKey: strings.Repeat("c", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("d", 64)}, ParentAlertID: "parent", ParentFingerprint: "parent-fp", State: "preparing", CreatedAt: at, UpdatedAt: at}
	for i := range count {
		id := fmt.Sprintf("member-%06d", 2*i)
		r.Members = append(r.Members, domain.MergeRelationMember{AlertID: id, State: "pending"})
		r.WaitMemberIDs = append(r.WaitMemberIDs, id, fmt.Sprintf("member-%06d", 2*i+1))
	}
	return r
}

func TestMergeLargeRelationAndOriginHaveNoMemberCountCap(t *testing.T) {
	for _, count := range []int{5000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r := largeMergeRelation(count)
			if err := r.Validate(); err != nil {
				t.Fatal("large sorted relation rejected", err)
			}
			origin := domain.MergeOrigin{OperationID: r.ID, Policy: r.Policy, WindowID: r.WindowID, MembersDigest: r.GroupKey, MemberCount: count}
			if err := origin.Validate(domain.BuiltinMergeEventSourceID); err != nil {
				t.Fatal("large origin count rejected", err)
			}
			r.WaitMemberIDs = r.WaitMemberIDs[1:]
			if err := r.Validate(); err == nil {
				t.Fatal("linear inclusion check lost missing first member")
			}
		})
	}
}

func BenchmarkMergeRelationValidate(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		r := largeMergeRelation(count)
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := r.Validate(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
