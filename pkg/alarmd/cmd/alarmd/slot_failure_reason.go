// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// slotFailureReason names a failed Slot's reason and, where the refusal has
// one, the apply site that produced it.
//
// internal_unknown is the answer for a failure this site looked at and could
// not name. It is meant to be rare, and every word added here was added
// because it was not: a failure that names itself one level down and is not
// asked about here arrives as an unclassified defect, and a deployment cannot
// tell one such failure from another or count how often either happens.
//
// A function of its own so the classification can be exercised without
// standing up a runtime. The block it came from decides a Slot's whole
// completion line, and a rule nobody can call is a rule nobody tests.
func slotFailureReason(err error) (observability.ReasonCode, string) {
	reason := observability.ReasonInternalUnknown
	gapApplySite := ""
	if errors.Is(err, access.ErrFrozenQueryPlanUnavailable) {
		reason = observability.ReasonContractDeterministic
	}
	// A gap guard conflict is a classifiable refusal, and it repeats on every
	// round for the same Query Group. internal_unknown is where a site that
	// looked at a failure and could not name it puts things; this one has a
	// name and carries the two values it compared.
	if conflict, named := worker.GapGuardConflictReason(err); named {
		reason = observability.ReasonCode(conflict)
	}
	if conflict, named := worker.StateConflictReason(err); named {
		reason = observability.ReasonCode(conflict)
	}
	// Two series batches disagreeing about one Plan's gap marker. The
	// Slot-wide merge resolves every other difference between batches and
	// refuses only this one, so without a word here the one shape it cannot
	// resolve is also the one nobody can count.
	if disagree, named := worker.GapGuardDisagreeReason(err); named {
		reason = observability.ReasonCode(disagree)
	}
	// Loaded Runtime State whose Level contract is not the compiled Plan's.
	// The error has carried this code in the query failure facts since it was
	// written; the completion line was not asking, so a deployment where every
	// Query Group reported it read as thousands of unclassified defects.
	var contractMismatch *execution.StateContractMismatchError
	if errors.As(err, &contractMismatch) {
		reason = observability.ReasonCode(contract.ReasonStateLevelContractMismatch)
	}
	// The trigger evaluator refusing its own state before deciding. Which
	// invariant failed stays in the error's text and on the line's operation
	// field; the word is the class, because one word per invariant is a
	// vocabulary nobody can hold.
	var triggerInvariant *trigger.InternalErrorV2
	if errors.As(err, &triggerInvariant) {
		reason = observability.ReasonCode(contract.ReasonTriggerInvariant)
	}
	// The gap marker store's own refusals, which are not the same as the
	// conflict above: that one is this Slot refusing before it writes, these
	// are the store refusing the write because the marker moved under it. Both
	// were internal_unknown, so a Query Group conflicting on every other Slot
	// for half an hour arrived as an unclassified defect that the same-Slot
	// retry then cleared.
	if refusal, named := worker.GapApplyReason(err); named {
		reason = observability.ReasonCode(refusal)
		// The site goes on the line as its own field, not only inside the
		// error text. Which of the Slot's two applies refused is the question
		// this refusal exists to answer, and an answer that has to be parsed
		// out of a sentence is one nobody can group or count by.
		gapApplySite = worker.GapApplySiteOf(err)
	}
	return reason, gapApplySite
}
