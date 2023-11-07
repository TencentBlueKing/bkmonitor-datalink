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
)

const (
	MaxSamplesConfigPath           = "promql.max_samples"
	TimeoutConfigPath              = "promql.timeout"
	LookbackDeltaConfigPath        = "promql.lookback_delta"
	EnableNegativeOffsetConfigPath = "promql.enable_negative_offset"
	EnableAtModifierConfigPath     = "promql.enable_at_modifier"
	DefaultStepConfigPath          = "promql.default_step"
)

var (
	MaxSamples           int
	Timeout              time.Duration
	LookbackDelta        time.Duration
	EnableNegativeOffset bool
	EnableAtModifier     bool
	DefaultStep          time.Duration
)
