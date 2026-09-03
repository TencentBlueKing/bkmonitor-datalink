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
	"encoding/json"
	"errors"
	"fmt"
)

func evaluatePingUnreachable(raw json.RawMessage) (pureDetectionStatus, error) {
	var value *float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return pureDetectionUnknown, fmt.Errorf("alarmd detect: invalid ping loss percent: %w", err)
	}
	if value == nil {
		return pureDetectionUnknown, errors.New("alarmd detect: ping loss percent is null")
	}
	if !finite(*value) || *value < 0 || *value > 1 {
		return pureDetectionUnknown, errors.New("alarmd detect: ping loss percent is outside [0,1]")
	}
	if *value >= 1 {
		return pureDetectionAnomalous, nil
	}
	return pureDetectionNormal, nil
}
