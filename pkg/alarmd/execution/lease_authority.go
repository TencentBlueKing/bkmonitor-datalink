// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"time"
)

// LeaseAuthority is the authority a Slot's side effects run under, as its
// holder sees it: the instant, on the holder's own clock, after which the
// holder no longer admits work on its lease. The ownership Session is one;
// its Deadline is the store's last renewal carried over to the holder's
// clock and, under a pending content change, capped at the change's
// effective time, so it is also the moment the content the Slot executes
// stops being authorized (decision-016).
//
// It is consulted before an output batch is started, not after: a Kafka
// write that has been acknowledged cannot be taken back, so the question
// "will this lease still be live when the batch has landed" is asked before
// the first byte, with the batch's own bound added.
type LeaseAuthority interface {
	Deadline() time.Time
}

type leaseAuthorityKey struct{}

// ContextWithLeaseAuthority carries the authority a Slot runs under to the
// sinks that write on its behalf. It travels on the context rather than on
// a port because it is a fact about this attempt, set by the runner that
// holds the Session; a sink that finds none admits as it always did, which
// is what every path without a lease -- tests, tools -- expects.
func ContextWithLeaseAuthority(ctx context.Context, authority LeaseAuthority) context.Context {
	if ctx == nil || authority == nil {
		return ctx
	}
	return context.WithValue(ctx, leaseAuthorityKey{}, authority)
}

// LeaseAuthorityFromContext returns the authority the runner attached, if any.
func LeaseAuthorityFromContext(ctx context.Context) (LeaseAuthority, bool) {
	if ctx == nil {
		return nil, false
	}
	authority, ok := ctx.Value(leaseAuthorityKey{}).(LeaseAuthority)
	return authority, ok && authority != nil
}
