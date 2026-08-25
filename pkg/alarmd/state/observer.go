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

type Operation string

const (
	OperationLoad  Operation = "LOAD"
	OperationWrite Operation = "WRITE"
)

type OperationResult string

const (
	OperationSucceeded OperationResult = "SUCCEEDED"
	OperationFailed    OperationResult = "FAILED"
)

type Observation struct {
	Operation     Operation
	Target        string
	Result        OperationResult
	ReasonCode    string
	Keys          int
	RequestBytes  int
	ResponseBytes int
	Duration      time.Duration
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
