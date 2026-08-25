// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	StageDetectCompleted = "detect_completed"

	ObservationSuccess  = "success"
	ObservationTerminal = "terminal"
	ObservationFailed   = "failed"
)

// Observation is the single bounded aggregate emitted for one Evaluate call.
// It deliberately excludes plan, Level, record and dimension identities so an
// M8 adapter cannot accidentally turn them into metric labels.
type Observation struct {
	Stage        string
	Result       string
	ReasonCode   string
	Completeness string
	Counts       DetectionCounts
	Duration     time.Duration
}

// Observer is implemented by M8. Observer failures must not alter Detect
// results; concrete metric and logging behavior remains outside this package.
type Observer interface {
	ObserveDetect(context.Context, Observation)
}

type ObserverFunc func(context.Context, Observation)

func (function ObserverFunc) ObserveDetect(ctx context.Context, observation Observation) {
	if function != nil {
		function(ctx, observation)
	}
}

func observeDetect(ctx context.Context, observer Observer, observation Observation) {
	if observer != nil {
		observer.ObserveDetect(ctx, observation)
	}
}

func finishDetectObservation(
	ctx context.Context,
	observer Observer,
	completeness string,
	counts DetectionCounts,
	returnErr error,
	duration time.Duration,
) {
	observation := Observation{
		Stage: StageDetectCompleted, Result: ObservationSuccess, Completeness: observedCompleteness(completeness),
		Counts: counts, Duration: duration,
	}
	if returnErr != nil {
		var budget *BudgetError
		if errors.As(returnErr, &budget) {
			observation.Result = ObservationTerminal
			observation.ReasonCode = contract.ReasonMessageBudgetExceeded
		} else {
			observation.Result = ObservationFailed
		}
	} else {
		switch completeness {
		case contract.QueryCompletenessPartial:
			observation.ReasonCode = contract.ReasonQueryPartial
		case contract.QueryCompletenessUnavailable:
			observation.ReasonCode = contract.ReasonQueryUnavailable
		}
	}
	observeDetect(ctx, observer, observation)
}

func observedCompleteness(completeness string) string {
	switch completeness {
	case contract.QueryCompletenessFull, contract.QueryCompletenessPartial, contract.QueryCompletenessUnavailable:
		return completeness
	default:
		return ""
	}
}
