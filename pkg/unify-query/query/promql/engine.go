// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	prom "github.com/prometheus/prometheus/promql"
)

// Params
type Params struct {
	MaxSamples           int
	Timeout              time.Duration
	LookbackDelta        time.Duration
	EnableNegativeOffset bool
	EnableAtModifier     bool
}

var GlobalEngine *prom.Engine

// NewEngine
func NewEngine(params *Params) {
	// engine的内容里有指标注册操作，所以无法重复注册，所以其参数不能改变
	// 且engine内部成员全为私有，也无法进行修改
	if GlobalEngine != nil {
		return
	}
	GlobalEngine = prom.NewEngine(prom.EngineOpts{
		Reg:                  prometheus.DefaultRegisterer,
		MaxSamples:           params.MaxSamples,
		Timeout:              params.Timeout,
		LookbackDelta:        params.LookbackDelta,
		EnableNegativeOffset: params.EnableNegativeOffset,
		EnableAtModifier:     params.EnableAtModifier,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 {
			return GetDefaultStep().Milliseconds()
		},
	})
}

// 设置promEngine默认步长
var defaultStep = time.Minute

// SetDefaultStep
func SetDefaultStep(t time.Duration) {
	defaultStep = t
}

// GetDefaultStep
func GetDefaultStep() time.Duration {
	if defaultStep == 0 {
		return time.Minute
	}
	return defaultStep
}
