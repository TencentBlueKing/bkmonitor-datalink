// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import "errors"

type osRestartInput struct {
	current                  float64
	previous                 *float64
	hasTenMinutePoint        bool
	hasTwentyFiveMinutePoint bool
}

func evaluateOSRestart(input osRestartInput) (pureDetectionStatus, error) {
	if !finite(input.current) {
		return pureDetectionUnknown, errors.New("alarmd detect: os restart current value is not finite")
	}
	if input.previous != nil && !finite(*input.previous) {
		return pureDetectionUnknown, errors.New("alarmd detect: os restart previous value is not finite")
	}
	if input.current <= 0 || input.current > 600 {
		return pureDetectionNormal, nil
	}
	if input.previous != nil && input.current >= *input.previous {
		return pureDetectionNormal, nil
	}
	if !input.hasTenMinutePoint && !input.hasTwentyFiveMinutePoint {
		return pureDetectionNormal, nil
	}
	return pureDetectionAnomalous, nil
}
