// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"time"
)

type ObservationStage string

const (
	StageDependencyLoaded ObservationStage = "dependency_loaded"
	StageStateCommitted   ObservationStage = "state_committed"
)

type Operation string

const (
	OperationLoad       Operation = "load"
	OperationDecode     Operation = "decode"
	OperationEncode     Operation = "encode"
	OperationWrite      Operation = "write"
	OperationSample     Operation = "sample"
	OperationTransition Operation = "transition"
)

type OperationResult string

const (
	OperationSucceeded OperationResult = "success"
	OperationPartial   OperationResult = "degraded"
	OperationFailed    OperationResult = "failed"
)

const (
	CodecNoneV1 = "NONE_V1"
)

// Observation is one bounded aggregate emitted per M4 callpoint. All counters
// are deliberately scalar and low cardinality; RuntimeKey, strategy, Level and
// dimension identities must never be copied into metric labels.
type Observation struct {
	Stage                 ObservationStage
	Operation             Operation
	Target                string
	Result                OperationResult
	ReasonCode            string
	Codec                 string
	BackendCalls          int
	RequestBytes          int
	ResponseBytes         int
	DecodeBytes           int
	EncodeBytes           int
	StateBytes            int
	TouchedKeys           int
	FoundKeys             int
	MissingKeys           int
	ResetCorruptKeys      int
	UnsupportedKeys       int
	UnavailableKeys       int
	NoopKeys              int
	PersistedKeys         int
	InvariantKeys         int
	TouchedPoints         int
	AppliedPoints         int
	NoopPoints            int
	UnavailablePoints     int
	TerminalPoints        int
	LateAcceptedPoints    int
	LateOutOfWindowPoints int
	FullSummaries         int
	WarmingSummaries      int
	GappedSummaries       int
	BudgetViolations      int
	InvariantViolations   int
	Duration              time.Duration
}

// Observer is implemented by M8. Target and Redis keys must remain trace/log
// fields and must not become unbounded metric labels.
type Observer interface {
	ObserveState(context.Context, Observation)
}

type ObserverFunc func(context.Context, Observation)

func (function ObserverFunc) ObserveState(ctx context.Context, observation Observation) {
	if function != nil {
		function(ctx, observation)
	}
}

func observeState(ctx context.Context, observer Observer, observation Observation) {
	if observer != nil {
		observer.ObserveState(ctx, observation)
	}
}
