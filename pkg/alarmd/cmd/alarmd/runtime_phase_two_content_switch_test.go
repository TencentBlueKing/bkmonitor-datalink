// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A Query Group let go because its lease ran out at a content switch reads as
// the expected transition it is, named CONTENT_SCOPE_MOVED; any other lost
// lease still reads as a failure. The release's own error joined to it does
// not change which.
func TestALeaseThatRanOutAtAContentSwitchReadsAsAnExpectedTransition(t *testing.T) {
	switched := &ownership.LeaseEndedAtContentSwitch{Err: ownership.ErrStaleFence}
	for _, test := range []struct {
		err    error
		result observability.Result
		reason observability.ReasonCode
	}{
		{switched, observability.ResultSuccess, "CONTENT_SCOPE_MOVED"},
		{errors.Join(switched, errors.New("release: connection refused")), observability.ResultSuccess, "CONTENT_SCOPE_MOVED"},
		{ownership.ErrStaleFence, observability.ResultFailed, "OWNERSHIP_STALE_FENCE"},
	} {
		if got := lostResult(test.err); got != test.result {
			t.Fatalf("lostResult(%v) = %s, want %s", test.err, got, test.result)
		}
		if got := ownershipObservationReason(test.err); got != test.reason {
			t.Fatalf("ownershipObservationReason(%v) = %s, want %s", test.err, got, test.reason)
		}
	}
}
