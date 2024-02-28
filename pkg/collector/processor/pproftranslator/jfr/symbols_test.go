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
	"testing"

	"github.com/grafana/jfr-parser/parser/types"
	"github.com/stretchr/testify/assert"
)

func TestMergeJVMGeneratedClasses(t *testing.T) {
	tests := []struct {
		name     string
		frame    string
		expected string
	}{
		{
			name:     "test GeneratedMethodAccessor",
			frame:    "jdk/internal/reflect/GeneratedMethodAccessor123",
			expected: "jdk/internal/reflect/GeneratedMethodAccessor_",
		},
		{
			name:     "test Lambda",
			frame:    "com.example.$$Lambda$123/0x12345",
			expected: "com.example.$$Lambda$_",
		},
		{
			name:     "test libzstd-jni",
			frame:    "/tmp/libzstd-jni-1.2.3-4.so",
			expected: "libzstd-jni-_.so",
		},
		{
			name:     "test amazonCorrettoCryptoProvider",
			frame:    "/tmp/libamazonCorrettoCryptoProviderNativeLibraries.1234567890abcdef/libcrypto.so",
			expected: "libamazonCorrettoCryptoProvider_.so",
		},
		{
			name:     "test asyncProfiler",
			frame:    "/tmp/libasyncProfiler-linux-x64-17b9a1d8156277a98ccc871afa9a8f69215f92.so",
			expected: "libasyncProfiler-_.so",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, mergeJVMGeneratedClasses(tt.frame))
		})
	}
}

func TestProcessSymbols(t *testing.T) {
	symbolList := &types.SymbolList{
		Symbol: []types.Symbol{
			{String: "jdk/internal/reflect/GeneratedMethodAccessor123"},
			{String: "com.example.$$Lambda$123/0x12345"},
			{String: "/tmp/libzstd-jni-1.2.3-4.so"},
			{String: "/tmp/libamazonCorrettoCryptoProviderNativeLibraries.1234567890abcdef/libcrypto.so"},
			{String: "/tmp/libasyncProfiler-linux-x64-17b9a1d8156277a98ccc871afa9a8f69215f92.so"},
		},
	}
	expected := &types.SymbolList{
		Symbol: []types.Symbol{
			{String: "jdk/internal/reflect/GeneratedMethodAccessor_"},
			{String: "com.example.$$Lambda$_"},
			{String: "libzstd-jni-_.so"},
			{String: "libamazonCorrettoCryptoProvider_.so"},
			{String: "libasyncProfiler-_.so"},
		},
	}

	processSymbols(symbolList)
	assert.Equal(t, expected, symbolList)
}
