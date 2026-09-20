// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func detectObserver(observer observability.Observer) detect.Observer {
	return detect.ObserverFunc(func(ctx context.Context, source detect.Observation) {
		result := observability.Result(observability.ResultSuccess)
		switch source.Result {
		case detect.ObservationTerminal:
			result = observability.ResultTerminal
		case detect.ObservationFailed:
			result = observability.Result(observability.ResultFailed)
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentDetect, Stage: observability.StageDetectCompleted,
			Result: result, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonCode(source.ReasonCode), Duration: source.Duration,
			Counts: observability.Counts{
				Records: int64(source.Counts.EvaluatedRecords), Plans: int64(source.Counts.Plans),
				Levels: int64(source.Counts.CompiledLevels), Bytes: int64(source.Counts.EstimatedResultBytes),
			},
		})
	})
}

func observeRuntime(ctx context.Context, observer observability.Observer, observation observability.Observation) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer.Observe(ctx, observation)
}
