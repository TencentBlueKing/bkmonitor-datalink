// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"math"
)

// intMathCeil 计算 a 除以 b 的向上取整结果
// 参数:
//   - a: 被除数
//   - b: 除数
//
// 返回: a/b 的向上取整值（int64 类型）
// 示例: intMathCeil(10, 3) = 4, intMathCeil(10, 2) = 5
func intMathCeil(a, b int64) int64 {
	return int64(math.Ceil(float64(a) / float64(b)))
}

// intMathFloor 计算 a 除以 b 的向下取整结果
// 参数:
//   - a: 被除数
//   - b: 除数，如果为 0 则直接返回 a（避免除零错误）
//
// 返回: a/b 的向下取整值（int64 类型），如果 b 为 0 则返回 a
// 示例: intMathFloor(10, 3) = 3, intMathFloor(10, 2) = 5, intMathFloor(10, 0) = 10
func intMathFloor(a, b int64) int64 {
	if b == 0 {
		return a
	}
	return int64(math.Floor(float64(a) / float64(b)))
}
