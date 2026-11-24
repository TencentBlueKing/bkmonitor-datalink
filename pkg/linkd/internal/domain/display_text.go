// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import "strings"

// DisplayTextMaxChars 是名称和对象展示文本的上限，不改变身份字段或内容字段限制。
const DisplayTextMaxChars = 2000

// TruncateDisplayText 只遍历到截断点，不转换整段 rune 数组、不序列化对象。
func TruncateDisplayText(text string) string {
	if len(text) <= DisplayTextMaxChars {
		return text
	}
	count := 0
	for offset := range text {
		if count == DisplayTextMaxChars {
			return strings.Clone(text[:offset])
		}
		count++
	}
	return text
}
