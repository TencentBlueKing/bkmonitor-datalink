// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

// StageHandoverDrained names one planned handover's wait for the moved Query
// Group's Slot before its lease is released (SlotDrain, site handover). A
// stop's wait for every Slot is carried on the shutdown line instead (site
// shutdown), which is written once per process.
const StageHandoverDrained = "handover_drained"

// StageOutputUnapplied names one Slot whose events the broker acknowledged
// and whose State write the ownership store then refused (ReasonCode is the
// refusal): the next owner redoes the Slot from Progress and sends the same
// events again. Written by the Slot that holds both facts, once per Slot.
const StageOutputUnapplied = "output_unapplied"

// The places a drain is waited for, closed.
const (
	SlotDrainSiteHandover = "handover"
	SlotDrainSiteShutdown = "shutdown"
)

// The drain outcomes, closed.
const (
	// SlotDrainIdle: nothing was in flight; the release did not wait.
	SlotDrainIdle = "idle"
	// SlotDrainFinished: everything in flight returned inside the bound.
	SlotDrainFinished = "finished"
	// SlotDrainLeaseEnded: a handover's lease stopped renewing first -- its
	// grace ran out or the store refused it -- so the Slot's later writes are
	// refused and the next owner can redo it.
	SlotDrainLeaseEnded = "lease_ended"
	// SlotDrainDeadline: a stop's drain deadline passed and what was still
	// running was cancelled.
	SlotDrainDeadline = "deadline"
	// SlotDrainDeadlineUnreturned: as deadline, and some of what was
	// cancelled had still not returned a moment later - a call that does not
	// watch its context. The stop went on without it: its leases were
	// released and its later writes are refused by the fence.
	SlotDrainDeadlineUnreturned = "deadline_unreturned"
	// SlotDrainStopped: a handover was still waiting when the process began
	// to stop; the stop's own drain covers the Slot.
	SlotDrainStopped = "stopped"
)

// SlotDrainOutcomes is every outcome, for closed label sets.
var SlotDrainOutcomes = []string{
	SlotDrainIdle, SlotDrainFinished, SlotDrainLeaseEnded, SlotDrainDeadline, SlotDrainDeadlineUnreturned, SlotDrainStopped,
}

// StageExecutionHung names one execution found past its deadline and grace
// and its Query Group declined on this replica, or such an execution
// returning at last and the decline lifted (ExecutionHung).
const StageExecutionHung = "execution_hung"

// The hung-execution outcomes, closed.
const (
	ExecutionHungDeclined = "declined"
	ExecutionHungReturned = "returned_after_decline"
)

// ExecutionHungOutcomes is every outcome, for closed label sets.
var ExecutionHungOutcomes = []string{ExecutionHungDeclined, ExecutionHungReturned}

// ExecutionHungFacts is one execution that ignored its deadline and its
// cancellation before it began to commit (design 02 section 6.5): the stage
// it last entered - execute, query or evaluate, which points at the call that
// did not return - the deadline it ran under (its own: a normal Slot's query
// deadline, a recovery's counted from its arrival) and how far past it, and
// on return how long its Query Group was declined.
type ExecutionHungFacts struct {
	Outcome           string `json:"outcome"`
	Stage             string `json:"stage,omitempty"`
	DeadlineUnixMilli int64  `json:"deadline_ms"`
	PastDeadlineMS    int64  `json:"past_deadline_ms"`
	HungMS            int64  `json:"hung_ms,omitempty"`
}

// DeclinedEverywhereSample is one Query Group every eligible worker
// declines: how many did (Hops), and each as worker:stage.
type DeclinedEverywhereSample struct {
	QueryGroup string   `json:"query_group"`
	Hops       int      `json:"hops"`
	Stages     []string `json:"stages,omitempty"`
}

// SlotDrainFacts is one wait for in-flight Slots before a lease or a process
// let go (design 02 §6.2, §6.6). A Slot that outlives the wait can have had
// its events acknowledged and its State refused, and the next owner sends
// those events again: lease_ended and deadline are where that can happen,
// and output_unapplied_total counts it when it does.
type SlotDrainFacts struct {
	Site    string `json:"site"`
	Outcome string `json:"outcome"`
	// Waited is how many Slots were in flight when the wait began, and
	// Cancelled how many were still running when a stop's deadline passed.
	Waited    int `json:"waited"`
	Cancelled int `json:"cancelled"`
	// Unreturned is how many of the cancelled had not returned when the stop
	// went on without them.
	Unreturned int   `json:"unreturned"`
	WaitMS     int64 `json:"wait_ms"`
}
