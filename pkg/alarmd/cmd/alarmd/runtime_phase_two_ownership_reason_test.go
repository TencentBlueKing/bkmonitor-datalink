// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// An ownership observation names the store's refusal by the store's word:
// none for no error, the refusal for one of the four, internal_unknown for
// anything else. Every ownership site said internal_unknown for all four, so
// "is CONTENT_MOVED still zero, are STALE and NOT_DESIRED the same size as
// last release" could only be answered by grepping rate-limited log text.
func TestOwnershipObservationsNameTheStoresRefusal(t *testing.T) {
	for _, test := range []struct {
		err  error
		want observability.ReasonCode
	}{
		{nil, observability.ReasonNone},
		{ownership.ErrStaleFence, observability.ReasonCode(contract.ReasonOwnershipStaleFence)},
		{fmt.Errorf("release: %w", ownership.ErrNotDesired), observability.ReasonCode(contract.ReasonOwnershipNotDesired)},
		{ownership.ErrLeaseBusy, observability.ReasonCode(contract.ReasonOwnershipLeaseBusy)},
		{ownership.ErrContentScopeMoved, observability.ReasonCode(contract.ReasonContentScopeMoved)},
		{errors.New("dial tcp: i/o timeout"), observability.ReasonInternalUnknown},
	} {
		if got := ownershipObservationReason(test.err); got != test.want {
			t.Errorf("ownershipObservationReason(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	// And the production helper puts it on the line, with the result it had.
	var seen []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		seen = append(seen, observation)
	})
	observeProductionOwnership(context.Background(), observer, observability.StageLeaseRenewed, fmt.Errorf("renew: %w", ownership.ErrStaleFence))
	observeProductionOwnership(context.Background(), observer, observability.StageLeaseRenewed, nil)
	if len(seen) != 2 || seen[0].Result != observability.ResultFailed || string(seen[0].ReasonCode) != contract.ReasonOwnershipStaleFence ||
		seen[1].Result != observability.ResultSuccess || seen[1].ReasonCode != observability.ReasonNone {
		t.Fatalf("production ownership lines = %+v, want a failed one naming the stale fence and a clean success", seen)
	}
}
