// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package precision

import (
	stdJson "encoding/json"
	"strings"

	"github.com/spf13/cast"
)

func ProcessNumber(num stdJson.Number) any {
	numStr := num.String()

	// 处理空字符串
	if numStr == "" {
		return numStr
	}

	// 检查是否包含浮点数特征
	if strings.ContainsAny(numStr, ".eE") {
		if floatVal, err := cast.ToFloat64E(numStr); err == nil {
			return floatVal
		}
		return numStr
	}

	processList := []func(string) (any, error){
		func(s string) (any, error) { return cast.ToIntE(s) }, // 优先尝试转换为 int
		func(s string) (any, error) { return cast.ToUintE(s) },
		func(s string) (any, error) { return cast.ToInt64E(s) },
		func(s string) (any, error) { return cast.ToUint64E(s) },
		func(s string) (any, error) { return cast.ToFloat64E(s) },
	}

	for _, processor := range processList {
		if result, err := processor(numStr); err == nil {
			return result
		}
	}

	return numStr
}

// ProcessValue 递归处理值，要开启json解码时的stdJson.UseNumber选项
func ProcessValue(v any) any {
	switch nv := v.(type) {
	case map[string]any:
		processed := make(map[string]any)
		for k, val := range nv {
			processed[k] = ProcessValue(val)
		}
		return processed
	case []any:
		processed := make([]any, len(nv))
		for i, val := range nv {
			processed[i] = ProcessValue(val)
		}
		return processed
	case stdJson.Number:
		// 使用精度处理器处理数字，保持大数字的精度
		return ProcessNumber(nv)
	default:
		return v
	}
}
