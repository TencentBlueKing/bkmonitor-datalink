// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package opmatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatched(t *testing.T) {
	tests := []struct {
		op       Op
		content  string
		excepted string
		pass     bool
	}{
		{op: OpContains, content: "believe", excepted: "lie", pass: true},
		{op: OpContains, content: "believe", excepted: "liex", pass: false},
		{op: OpContains, content: "believe", excepted: "", pass: true},

		{op: OpNContains, content: "believe", excepted: "lie", pass: false},
		{op: OpNContains, content: "believe", excepted: "liex", pass: true},
		{op: OpNContains, content: "believe", excepted: "", pass: false},

		{op: OpEq, content: "ok", excepted: "ok", pass: true},
		{op: OpEq, content: "ok", excepted: "!ok", pass: false},
		{op: OpEq, content: "", excepted: "", pass: true},

		{op: OpReg, content: "ok4", excepted: "ok\\d", pass: true},
		{op: OpReg, content: "ok4", excepted: "ok\\d?[", pass: false},
		{op: OpReg, content: "ok", excepted: "!ok", pass: false},
		{op: OpReg, content: "", excepted: "", pass: true},

		{op: OpNq, content: "ok", excepted: "ok", pass: false},
		{op: OpNq, content: "ok", excepted: "!ok", pass: true},
		{op: OpNq, content: "", excepted: "", pass: false},

		{op: OpStartsWith, content: "golang", excepted: "go", pass: true},
		{op: OpStartsWith, content: "golang", excepted: "python", pass: false},
		{op: OpStartsWith, content: "golang", excepted: "", pass: true},

		{op: OpNStartsWith, content: "golang", excepted: "go", pass: false},
		{op: OpNStartsWith, content: "golang", excepted: "python", pass: true},
		{op: OpNStartsWith, content: "golang", excepted: "", pass: false},

		{op: OpEndsWith, content: "golang", excepted: "lang", pass: true},
		{op: OpEndsWith, content: "golang", excepted: "python", pass: false},
		{op: OpEndsWith, content: "golang", excepted: "", pass: true},

		{op: OpNEndsWith, content: "golang", excepted: "lang", pass: false},
		{op: OpNEndsWith, content: "golang", excepted: "python", pass: true},
		{op: OpNEndsWith, content: "golang", excepted: "", pass: false},

		{op: "unknown", content: "", excepted: "", pass: false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.pass, Match(tt.content, tt.excepted, string(tt.op)))
	}
}
