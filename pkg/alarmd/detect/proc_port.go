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
	"math"
	"strconv"
	"strings"
)

type procPortInput struct {
	procExists json.RawMessage
	dimensions map[string]json.RawMessage
}

func evaluateProcPort(input procPortInput) (pureDetectionStatus, error) {
	exists, err := procPortExists(input.procExists)
	if err != nil {
		return pureDetectionUnknown, err
	}
	if !exists {
		return pureDetectionAnomalous, nil
	}

	nonListening, err := procPortDimensionHasValue(input.dimensions, "nonlisten")
	if err != nil {
		return pureDetectionUnknown, err
	}
	if nonListening {
		return pureDetectionAnomalous, nil
	}

	notAccurate, err := procPortDimensionHasValue(input.dimensions, "not_accurate_listen")
	if err != nil {
		return pureDetectionUnknown, err
	}
	if notAccurate {
		return pureDetectionAnomalous, nil
	}
	return pureDetectionNormal, nil
}

func procPortExists(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, errors.New("alarmd detect: proc port value is missing")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("alarmd detect: invalid proc port value: %w", err)
	}
	switch typed := value.(type) {
	case float64:
		if !finite(typed) {
			return false, errors.New("alarmd detect: proc port value is not finite")
		}
		return math.Trunc(typed) == 1, nil
	case string:
		integer, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return false, fmt.Errorf("alarmd detect: invalid proc port integer: %w", err)
		}
		return integer == 1, nil
	default:
		return false, errors.New("alarmd detect: proc port value is not numeric")
	}
}

func procPortDimensionHasValue(dimensions map[string]json.RawMessage, name string) (bool, error) {
	raw, ok := dimensions[name]
	if !ok {
		return false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("alarmd detect: invalid proc port dimension %s: %w", name, err)
	}
	return value != "[]" && value != "null", nil
}
