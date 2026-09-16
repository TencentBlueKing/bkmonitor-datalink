// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// QueryProvider is the only external query boundary. The phase-two Access
// module provides the UQ implementation without importing UQ server modules.
type QueryProvider interface {
	Execute(context.Context, QueryAttempt, ProviderSeriesSink) (ProviderCompletion, error)
}

// QueryExecutionSource streams one frozen execution to the Coordinator. Access
// never receives evaluation or side-effect ports.
type QueryExecutionSource interface {
	Execute(context.Context, QueryExecutionRequest, QueryExecutionConsumer) (QueryExecutionCompletion, error)
}

type SequencingScope struct {
	Slot      SlotIdentity
	StateKeys []StateKeyIdentity
	GapKeys   []PlanGapIdentity
}

type SideEffectSequencer interface {
	Sequence(context.Context, SequencingScope, func(context.Context) error) error
}

type Evaluator interface {
	Evaluate(context.Context, EvaluationRequest) (EvaluationResult, error)
}

// SideEffectAdmitter checks current ownership and activation facts. Its result
// is intentionally not a reusable ticket; the worker checks it again before
// publishing events.
type SideEffectAdmitter interface {
	Check(context.Context, SideEffectAdmissionRequest) (SideEffectAdmissionResult, error)
}

type GapGuardStore interface {
	LoadGaps(context.Context, GapLoadRequest) (GapLoadResult, error)
	// LoadGapsInto emits one validated fact at a time, in request order. It
	// does not retain emitted facts and stops before the next read when accept
	// fails or the context is canceled. The caller owns accepted references.
	LoadGapsInto(context.Context, GapLoadRequest, func(GapGuardSnapshot) error) error
	ApplyGap(context.Context, GapGuardApplyRequest) (GapGuardApplyResult, error)
}

// HostBusiness answers which business the CMDB index holds a host under.
//
// One host per call rather than a batch, because the index is an in-process
// snapshot and the call is a map read: a batch interface would suggest a round
// trip that is not there and would need its own partial-answer semantics for a
// failure that cannot happen.
//
// The three answers are distinct and the caller needs all three. A host CMDB
// holds under this business is expected; one it holds under another has left,
// which is what stops a departed host being reported absent forever; and one it
// does not hold is neither - not expected, and not something that left.
type HostBusiness interface {
	// LookupHostBusiness returns the business the host identity belongs to, and
	// false when the index does not hold it. The identity is "address|cloud".
	LookupHostBusiness(identity string) (string, bool)
	// HostIndexResolved reports whether this process holds an index it can
	// answer from at all.
	//
	// It is separate from the lookup because the lookup cannot carry it. "Not
	// held" is the safe answer for one host -- a host nobody has heard of is
	// not expected -- but the same answer given to every host, because no
	// index has been built, is not an answer about hosts at all, and a caller
	// that cannot tell the two apart reads a cold index as a target that
	// resolved to nobody. It is a method rather than a field on the answer so
	// that a caller asking about a whole target asks once.
	HostIndexResolved() bool
}

// PlanNoDataStore holds what each Plan remembers about absence between Slots.
type PlanNoDataStore interface {
	LoadNoData(context.Context, NoDataLoadRequest) (NoDataLoadResult, error)
	ApplyNoData(context.Context, NoDataApplyRequest) (NoDataApplyResult, error)
}

type EventSink interface {
	// WriteBatch returns nil only after the whole batch has received its
	// broker ACK. A non-nil error means that ACK is unknown; callers must not
	// advance Runtime State or Progress.
	WriteBatch(context.Context, []contract.TriggerEventV1) error
}

// OpenAlertCopy is the process copy of the alert consumer's open alert set,
// as the worker drives it: the trigger asks it through contract.OpenAlertSet
// on every evaluation, the worker tells it which Plans are about to be
// evaluated so their strategies are in its next read, and tells it which
// envelopes the sink took so it can keep itself current while the
// consumer's publication is unavailable.
type OpenAlertCopy interface {
	contract.OpenAlertSet
	TrackPlans([]PlanIdentity)
	Acknowledged([]contract.TriggerEventV1)
}

type StateStore interface {
	LoadRuntime(context.Context, StatePreflightRequest) (StatePreflightResult, error)
	AdmitRuntime(context.Context, StateApplyRequest) (StateAdmissionResult, error)
	ApplyRuntime(context.Context, StateApplyRequest) (StateApplyResult, error)
}

type ProgressStore interface {
	LoadProgress(context.Context, ProgressIdentity) (ProgressLoadResult, error)
	BeginSlot(context.Context, ProgressBeginRequest) (ProgressBeginResult, error)
	CommitProgress(context.Context, ProgressCommitRequest) (ProgressCommitResult, error)
}

type Observer = observability.Observer
