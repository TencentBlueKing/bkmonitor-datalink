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

	if intVal, err := cast.ToIntE(numStr); err == nil {
		return intVal
	}

	if uintVal, err := cast.ToUintE(numStr); err == nil {
		return uintVal
	}

	if int64Val, err := cast.ToInt64E(numStr); err == nil {
		return int64Val
	}

	if uint64Val, err := cast.ToUint64E(numStr); err == nil {
		return uint64Val
	}

	if floatVal, err := cast.ToFloat64E(numStr); err == nil {
		return floatVal
	}

	return numStr
}
