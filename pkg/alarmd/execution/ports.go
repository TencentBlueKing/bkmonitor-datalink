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
	Execute(context.Context, QueryAttempt) (ProviderResult, error)
}

// QueryExecutionSource assembles one complete InternalExecution from Go
// Access results.
type QueryExecutionSource interface {
	Execute(context.Context, QueryExecutionRequest) (QueryExecutionResult, error)
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
	ApplyGap(context.Context, GapGuardApplyRequest) (GapGuardApplyResult, error)
}

type EventSink interface {
	// WriteBatch returns nil only after the whole batch has received its
	// broker ACK. A non-nil error means that ACK is unknown; callers must not
	// advance Runtime State or Progress.
	WriteBatch(context.Context, []contract.TriggerEventV1) error
}

type StateStore interface {
	LoadRuntime(context.Context, StatePreflightRequest) (StatePreflightResult, error)
	AdmitRuntime(context.Context, StateApplyRequest) (StateAdmissionResult, error)
	ApplyRuntime(context.Context, StateApplyRequest) (StateApplyResult, error)
}

type ProgressStore interface {
	LoadProgress(context.Context, ProgressNamespace) (ScheduleProgress, error)
	CommitProgress(context.Context, ProgressCommitRequest) (ProgressCommitResult, error)
}

type Observer = observability.Observer
