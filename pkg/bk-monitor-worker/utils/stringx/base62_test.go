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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeIdentifier(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		// 取自 SDK eco/go/sdk/base/model/name_format_test.go，保证与上报侧编码一致
		{"base62Hap5tiL5", "中文"},
		{"base62pUCK", "(%)"},
		{"base62pUCKgcojnjKlnf3eSOI1B3Y", "cpu 使用率 (%)"},
		{"base629uZ5tiL5tQ3clRH", "test-中国"},
		{"base629uZ5tiL5tEG", "a-中国"},
		{
			"base62H645oS55ftXyxczzDc0zJUzzrs3yddzzBpFQyN00P1RzjczzP0SzbFXyBpFQus1ybEyyr8V0XsWzB",
			"测试名字 - 中文监控项 - 目录的磁盘使用率",
		},
		// 满足 [a-zA-Z0-9_] 的名字 SDK 不会编码，不能被改写
		{"namespace", "namespace"},
		{"service_name", "service_name"},
		{"skuId", "skuId"},
		{"3rd_party", "3rd_party"},
		{"", ""},
		// SDK 只按前缀区分编码字段，用户命名成 base62xxx 时解出来是乱码，需保持原样
		{"base62", "base62"},
		{"base62_test", "base62_test"},
		{"base62abc", "base62abc"},
		{"base62Id", "base62Id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DecodeIdentifier(tt.name))
		})
	}
}
