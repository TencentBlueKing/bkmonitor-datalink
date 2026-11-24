// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// MaxEventValues 限制单条事件的观测字段数量，避免无界动态载荷。
const MaxEventValues = 256

// EventValues 保存本次事件的数值快照，不参与业务关联身份计算。
// 数值采用有限 float64，与现有领域数字精度一致；缺失字段不补零。
type EventValues map[string]float64

// Validate 校验观测字段数量、名称和数值。
func (v EventValues) Validate() error {
	if len(v) > MaxEventValues {
		return fmt.Errorf("values must not exceed %d fields", MaxEventValues)
	}
	for key, value := range v {
		if err := validateTextLength("values key", key, 1, 256); err != nil {
			return err
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("values must contain only finite numbers")
		}
	}
	return nil
}

// Clone 返回不共享 map 的副本，并将缺失与空对象统一为空对象。
func (v EventValues) Clone() EventValues {
	result := make(EventValues, len(v))
	for key, value := range v {
		result[key] = value
	}
	return result
}

// UnmarshalJSON 拒绝字段中的 null、字符串、布尔值及嵌套值，避免 null 被解码为零。
func (v *EventValues) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode values: %w", err)
	}
	result := make(EventValues, len(fields))
	for key, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("values must contain only finite numbers")
		}
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			return fmt.Errorf("values must contain only finite numbers")
		}
		result[key] = number
	}
	if err := result.Validate(); err != nil {
		return err
	}
	*v = result
	return nil
}
