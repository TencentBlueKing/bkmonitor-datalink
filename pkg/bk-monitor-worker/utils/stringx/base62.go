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
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jxskiss/base62"
)

// Base62Prefix 上报 SDK 对非法字段名编码后使用的前缀
const Base62Prefix = "base62"

// DecodeIdentifier 还原上报 SDK 编码前的原始字段名，非编码字段名原样返回。
//
// SDK 会把不满足 [a-zA-Z0-9_] 的指标名、维度名用 base62 编码成带 base62 前缀的标识符，存储与查询都
// 使用编码后的名字，原始名需要在展示前解码还原。SDK 仅以前缀区分编码字段，用户把字段命名成 base62xxx
// 时同样会命中前缀，这类字段解出来是乱码，因此要求解码结果可打印，否则视为未编码。
func DecodeIdentifier(name string) string {
	if !strings.HasPrefix(name, Base62Prefix) {
		return name
	}

	decoded, err := base62.Decode([]byte(name[len(Base62Prefix):]))
	if err != nil {
		return name
	}

	original := string(decoded)
	if original == "" || original == name || !isPrintable(original) {
		return name
	}
	return original
}

// isPrintable 解码结果必须是合法 UTF-8 且全部可打印，否则说明这不是 SDK 编码出来的名字
func isPrintable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
