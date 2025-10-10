// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stringx

import (
	"reflect"
	"regexp"
	"strings"
	"unsafe"
)

// String2byte converts string to a byte without memory allocation.
func String2byte(s string) []byte {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := reflect.SliceHeader{
		Data: sh.Data,
		Len:  sh.Len,
		Cap:  sh.Len,
	}
	return *(*[]byte)(unsafe.Pointer(&bh))
}

// 判断是否为空字符串
func IsEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

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

// LimitLengthPrefix 从前向后截取一定长度字符串
func LimitLengthPrefix(input string, length int) string {
	if length <= 0 {
		return ""
	}
	if length > len(input) {
		return input
	}
	return input[:length]
}

// LimitLengthSuffix 从后向前截取一定长度字符串
func LimitLengthSuffix(input string, length int) string {
	if length <= 0 {
		return ""
	}
	index := len(input) - length
	if index < 0 {
		index = 0
	}
	return input[index:]
}

var (
	matchFirstCap = regexp.MustCompile("([a-z0-9])([A-Z])")
	matchAllCap   = regexp.MustCompile("([A-Z]+)([A-Z][a-z])")
)

// CamelToSnake 转换驼峰格式为下划线
func CamelToSnake(s string) string {
	// 先替换，比如把 "IDNumber" 替换为 "ID_Number"
	snake := matchAllCap.ReplaceAllString(s, "${1}_${2}")
	// 再替换，例如把 "ID_Number" 替换为 "i_d_number"
	snake = matchFirstCap.ReplaceAllString(snake, "${1}_${2}")
	// 将字符串转换为小写并返回
	return strings.ToLower(snake)
}


// SnakeToCamel 转换下划线格式为驼峰格式
func SnakeToCamel(s string) string {
	if s == "" {
		return s
	}

	// 如果已经是 camelCase 或 PascalCase，直接返回
	if !strings.Contains(s, "_") && s[0] >= 'a' && s[0] <= 'z' {
		return s
	}

	// 分割字符串并处理每个部分
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return s
	}

	// 第一个部分保持小写
	result := strings.ToLower(parts[0])

	// 后续部分首字母大写
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			part := strings.ToLower(parts[i])
			if len(part) > 0 {
				result += strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}

	return result
}
