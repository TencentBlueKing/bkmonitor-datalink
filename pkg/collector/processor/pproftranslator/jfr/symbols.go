// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jfr

import (
	"regexp"

	"github.com/grafana/jfr-parser/parser/types"
)

var replacements = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern:     regexp.MustCompile("^(jdk/internal/reflect/GeneratedMethodAccessor)(\\d+)$"),
		replacement: "${1}_",
	},
	{
		pattern:     regexp.MustCompile("^(.+\\$\\$Lambda\\$)\\d+[./](0x[\\da-f]+|\\d+)$"),
		replacement: "${1}_",
	},
	{
		pattern:     regexp.MustCompile("^(\\.?/tmp/)?(libzstd-jni-\\d+\\.\\d+\\.\\d+-)(\\d+)(\\.so)( \\(deleted\\))?$"),
		replacement: "libzstd-jni-_.so",
	},
	{
		pattern: regexp.MustCompile("^(\\.?/tmp/)?(lib)?(amazonCorrettoCryptoProvider)(NativeLibraries\\.)?([0-9a-f]{16})" +
			"(/libcrypto|/libamazonCorrettoCryptoProvider)?(\\.so)( \\(deleted\\))?$"),
		replacement: "libamazonCorrettoCryptoProvider_.so",
	},
	{
		pattern: regexp.MustCompile("^(\\.?/tmp/)?(libasyncProfiler)-(linux-arm64|linux-musl-x64|linux-x64|macos)-" +
			"(17b9a1d8156277a98ccc871afa9a8f69215f92)(\\.so)( \\(deleted\\))?$"),
		replacement: "libasyncProfiler-_.so",
	},
}

func mergeJVMGeneratedClasses(frame string) string {
	for _, r := range replacements {
		frame = r.pattern.ReplaceAllString(frame, r.replacement)
	}
	return frame
}

// processSymbols 使用正则处理 JVM 的特定类名模式
func processSymbols(ref *types.SymbolList) {
	for i := range ref.Symbol {
		ref.Symbol[i].String = mergeJVMGeneratedClasses(ref.Symbol[i].String)
	}
}
