// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestMemberPoolSeparatesTenantsAndIgnoresRetiredStageResults(t *testing.T) {
	p := NewMemberPool()
	one := MemberBatch{Key: "batch", Phase: "link", State: "pending", Facts: domain.MergeMemberContext{TenantID: "one", ID: "relation"}}
	two := one
	two.Facts.TenantID = "two"
	p.Stage([]MemberBatch{one})
	p.Stage([]MemberBatch{two})
	if got := p.Take(2, time.Now()); len(got) != 2 {
		t.Fatal("tenant stages collided", got)
	}
	one.State = "completed"
	if !p.Finish(one, nil, time.Now()) {
		t.Fatal("first tenant did not complete")
	}
	if _, done := p.Stage([]MemberBatch{two}); done {
		t.Fatal("completion crossed tenant scope")
	}
	p.forget("two", "relation", "link")
	two.State = "completed"
	if p.Finish(two, nil, time.Now()) {
		t.Fatal("late result reopened retired stage")
	}
	if len(p.stages) != 1 {
		t.Fatal("retired stage retained")
	}
}
