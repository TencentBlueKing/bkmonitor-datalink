// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// withHeldBy carries what the previous round did into this one.
//
// A context value rather than a parameter because the places that report it
// -- the Slot source, which decides to give a Slot up, the executor, which
// reports the completion, and the Coordinator's Progress commit, which is
// the completion the fleet reads -- are reached through interfaces the
// Runner does not own. Threading it through those signatures would put a
// diagnostic field in contracts that have nothing else to do with it, and
// every implementation of those interfaces would have to carry it whether
// or not it reports anything. The key lives in observability so the worker,
// which cannot import the scheduler, reads the same one.
func withHeldBy(ctx context.Context, facts observability.HeldByFacts) context.Context {
	return observability.ContextWithHeldBy(ctx, facts)
}

// HeldByFromContext is what the previous round did with this Query Group;
// see observability.HeldByFromContext, which it is. Kept exported here for
// the runtime's executor wrapper, which reports the completion line.
func HeldByFromContext(ctx context.Context) *observability.HeldByFacts {
	return observability.HeldByFromContext(ctx)
}

// rememberHeldBy stores this round's decision for the next one.
//
// A round that ran the Slot held nothing back, so it is remembered as the word
// for that rather than as itself: the question the next Slot answers is "what
// kept me from running", and "the previous round ran" is not an answer to it.
// Every other word is kept as run_one_return_total counts it, so the two
// readings share one vocabulary.
//
// A readiness deferral is not a round that ran. Execute was entered and handed
// back by access with an instant to wait for, and the Slot did not run -- so
// "the readiness wait" is exactly the answer the next Slot needs, and folding
// it into "nothing held me" is how it stops being available. It keeps its own
// word and carries the instant it was told to wait for, taken from the same
// accessor the due bound uses for this decision so the two cannot disagree.
func (runner *Runner) rememberHeldBy(decision string) {
	if runner == nil {
		return
	}
	word := decision
	switch decision {
	case "execute", "execute_returned", "execution_returned":
		word = observability.HeldByNothing
	}
	if !observability.ValidHeldByDecision(word) {
		// A decision that is not in the published vocabulary is reported as
		// the catch-all the run outcome uses for the same case, never dropped:
		// a word nobody can read is still evidence that a path exists.
		word = "other_error"
	}
	facts := observability.HeldByFacts{Decision: word, AtUnixMilli: runner.now().UnixMilli()}
	if word == "query_cooldown" {
		facts.QueryCooldownFailures = runner.queryCooldown.failures
		if until := runner.queryCooldown.until; !until.IsZero() {
			facts.QueryCooldownUntilMilli = until.UnixMilli()
		}
	}
	if word == observability.HeldByReadinessDeferred {
		if readyAt := runner.NextReadyAt(); !readyAt.IsZero() {
			facts.ReadyAtUnixMilli = readyAt.UnixMilli()
		}
	}
	runner.heldBy = facts
}
