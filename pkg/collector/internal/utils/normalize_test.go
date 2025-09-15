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
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{
			input:  "foo.bar",
			output: "foo_bar",
		},
		{
			input:  "foo.bar.zzz",
			output: "foo_bar_zzz",
		},
		{
			input:  "foo.bar..",
			output: "foo_bar",
		},
		{
			input:  "TestApp.HelloGo.HelloGoObjAdapter.connectRate",
			output: "TestApp_HelloGo_HelloGoObjAdapter_connectRate",
		},
		{
			input:  "TestApp.HelloGo.exception_single_log_more_than_3M",
			output: "TestApp_HelloGo_exception_single_log_more_than_3M",
		},
		{
			input:  "TestApp.HelloGo.asyncqueue0",
			output: "TestApp_HelloGo_asyncqueue0",
		},
		{
			input:  "Exception-Log",
			output: "Exception_Log",
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.output, NormalizeName(tt.input))
	}
}

func benchmarkIsNormalized(b *testing.B, f func(string) bool) {
	tests := []struct {
		input     string
		validated bool
	}{
		{input: "foo.bar", validated: false},
		{input: "foo.bar.zzz", validated: false},
		{input: "foo.bar..", validated: false},
		{input: "TestApp_HelloGo_HelloGoObjAdapter_connectRate", validated: true},
		{input: "TestApp_HelloGo_HelloGoObjAdapter.connectRate", validated: false},
		{input: "TestApp.HelloGo.exception_single_log_more_than_3M", validated: false},
		{input: "TestApp_HelloGo_exception_single_log_more_than_3M", validated: true},
		{input: "TestApp.HelloGo.asyncqueue0", validated: false},
		{input: "Exception-Log", validated: false},
		{input: "┓(-´∀`-)┏", validated: false},
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for _, tt := range tests {
				ok := f(tt.input)
				if tt.validated != ok {
					b.Errorf("input=(%v), want '%v' but go '%v'", tt.input, tt.validated, ok)
				}
			}
		}
	})
}

func BenchmarkIsNormalizedFast(b *testing.B) {
	benchmarkIsNormalized(b, IsNameNormalized)
}

func BenchmarkIsNormalizedRegex(b *testing.B) {
	namePattern := regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")
	f := func(s string) bool {
		return namePattern.MatchString(s)
	}
	benchmarkIsNormalized(b, f)
}
