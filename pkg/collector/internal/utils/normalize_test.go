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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	type Case struct {
		Input  string
		Output string
	}

	cases := []Case{
		{
			Input:  "foo.bar",
			Output: "foo_bar",
		},
		{
			Input:  "foo.bar.zzz",
			Output: "foo_bar_zzz",
		},
		{
			Input:  "foo.bar..",
			Output: "foo_bar",
		},
		{
			Input:  "TestApp.HelloGo.HelloGoObjAdapter.connectRate",
			Output: "TestApp_HelloGo_HelloGoObjAdapter_connectRate",
		},
		{
			Input:  "TestApp.HelloGo.exception_single_log_more_than_3M",
			Output: "TestApp_HelloGo_exception_single_log_more_than_3M",
		},
		{
			Input:  "TestApp.HelloGo.asyncqueue0",
			Output: "TestApp_HelloGo_asyncqueue0",
		},
		{
			Input:  "Exception-Log",
			Output: "Exception_Log",
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Output, NormalizeName(c.Input))
	}
}
