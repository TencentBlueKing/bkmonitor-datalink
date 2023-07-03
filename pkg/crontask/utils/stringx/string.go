// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stringx

import "strings"

// StringInSlice 判断字符串是否存在 Slice 中
func StringInSlice(str string, list []string) bool {
	for _, item := range list {
		if item == str {
			return true
		}
	}
	return false
}

// SplitString 分割字符串, 允许半角逗号、分号、空格
func SplitString(str string) []string {
	str = strings.Replace(str, ";", ",", -1)
	str = strings.Replace(str, " ", ",", -1)
	return strings.Split(str, ",")
}

// SplitStringByDot 点分割字符串
func SplitStringByDot(str string) []string {
	return strings.Split(str, ".")
}
