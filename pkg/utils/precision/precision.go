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
	"strconv"
	"strings"
)

const (
	JavaScriptSafeIntegerMax = 9007199254740991
	JavaScriptSafeIntegerMin = -9007199254740991
)

func ProcessNumber(num stdJson.Number) any {
	numStr := num.String()

	// 检查是否像浮点数 - 对于浮点数，返回字符串保持原始格式
	if strings.ContainsAny(numStr, ".eE") {
		return numStr
	}

	if int64Val, err := strconv.ParseInt(numStr, 10, 64); err == nil {
		if int64Val >= JavaScriptSafeIntegerMin && int64Val <= JavaScriptSafeIntegerMax {
			return int64Val
		}
		return numStr
	}

	if _, err := strconv.ParseUint(numStr, 10, 64); err == nil {
		return numStr
	}

	return numStr
}
