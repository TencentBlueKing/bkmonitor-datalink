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
	"errors"
	"math"
)

type pureDetectionStatus string

const (
	pureDetectionUnknown   pureDetectionStatus = "UNKNOWN"
	pureDetectionNormal    pureDetectionStatus = "NORMAL"
	pureDetectionAnomalous pureDetectionStatus = "ANOMALOUS"
)

type simpleRingRatioInput struct {
	current      float64
	previous     *float64
	floorPercent *float64
	ceilPercent  *float64
}

func evaluateSimpleRingRatio(input simpleRingRatioInput) (pureDetectionStatus, error) {
	floorEnabled, err := validRatioSide(input.floorPercent)
	if err != nil {
		return pureDetectionUnknown, err
	}
	ceilEnabled, err := validRatioSide(input.ceilPercent)
	if err != nil {
		return pureDetectionUnknown, err
	}
	if !floorEnabled && !ceilEnabled {
		return pureDetectionUnknown, errors.New("alarmd detect: simple ring ratio requires floor or ceil")
	}
	if !finite(input.current) {
		return pureDetectionUnknown, errors.New("alarmd detect: simple ring ratio current value is not finite")
	}
	if input.previous == nil {
		return pureDetectionUnknown, nil
	}
	if !finite(*input.previous) {
		return pureDetectionUnknown, errors.New("alarmd detect: simple ring ratio previous value is not finite")
	}

	previous := *input.previous
	if input.current == 0 && previous == 0 {
		return pureDetectionNormal, nil
	}
	if floorEnabled && input.current <= previous*(100-*input.floorPercent)*0.01 {
		return pureDetectionAnomalous, nil
	}
	if ceilEnabled && input.current >= previous*(100+*input.ceilPercent)*0.01 {
		return pureDetectionAnomalous, nil
	}
	return pureDetectionNormal, nil
}

func validRatioSide(percent *float64) (bool, error) {
	if percent == nil {
		return false, nil
	}
	if !finite(*percent) || *percent < 0 {
		return false, errors.New("alarmd detect: simple ring ratio percentage is invalid")
	}
	return *percent > 0, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
