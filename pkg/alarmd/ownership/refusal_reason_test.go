// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The four refusals each have a name, a wrapped refusal keeps its name, and
// nothing else -- nil, an anonymous error, a conflict that is not a refusal
// -- gets one. The list a metric pre-registers is exactly the four names.
func TestRefusalReasonNamesTheFourRefusalsAndNothingElse(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{ErrStaleFence, contract.ReasonOwnershipStaleFence},
		{ErrNotDesired, contract.ReasonOwnershipNotDesired},
		{ErrLeaseBusy, contract.ReasonOwnershipLeaseBusy},
		{ErrContentScopeMoved, contract.ReasonContentScopeMoved},
		{fmt.Errorf("state: runtime state apply for qg: %w", ErrContentScopeMoved), contract.ReasonContentScopeMoved},
		{fmt.Errorf("alarmd worker: side-effect admission: %w", fmt.Errorf("wrapped twice: %w", ErrNotDesired)), contract.ReasonOwnershipNotDesired},
	} {
		reason, ok := RefusalReason(test.err)
		if !ok || reason != test.want {
			t.Errorf("RefusalReason(%v) = %q, %t; want %q", test.err, reason, ok, test.want)
		}
	}
	for _, err := range []error{nil, errors.New("dial tcp: connection refused"), ErrAssignmentConflict} {
		if reason, ok := RefusalReason(err); ok {
			t.Errorf("RefusalReason(%v) = %q, want no name", err, reason)
		}
	}
	named := map[string]bool{}
	for _, err := range []error{ErrStaleFence, ErrNotDesired, ErrLeaseBusy, ErrContentScopeMoved} {
		reason, _ := RefusalReason(err)
		named[reason] = true
	}
	if len(RefusalReasons) != 4 || len(named) != 4 {
		t.Fatalf("RefusalReasons = %v, named = %v; want the same four", RefusalReasons, named)
	}
	for _, reason := range RefusalReasons {
		if !named[reason] {
			t.Errorf("RefusalReasons lists %q, which no refusal maps to", reason)
		}
	}
}
