// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"sort"
	"strings"
	"unicode"
)

func MatchSubLabels(subset, set map[string]string) bool {
	for k, v := range subset {
		val, ok := set[k]
		if !ok || val != v {
			return false
		}
	}
	return true
}

func NormalizeName(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }), "_")
}

func MapToSelector(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}

	var selector []string
	for k, v := range m {
		selector = append(selector, k+"="+v)
	}
	sort.Strings(selector)
	return strings.Join(selector, ",")
}

func SelectorToMap(s string) map[string]string {
	selector := make(map[string]string)
	if len(s) == 0 {
		return selector
	}

	for _, part := range strings.Split(s, ",") {
		kv := strings.Split(strings.TrimSpace(part), "=")
		if len(kv) != 2 {
			continue
		}
		selector[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return selector
}
