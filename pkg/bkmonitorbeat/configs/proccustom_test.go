// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcCustomMatch(t *testing.T) {
	tests := []struct {
		Cmd     string
		Config  *ProcCustomConfig
		Matched bool
	}{
		{
			Cmd: "/app/foo/bar",
			Config: &ProcCustomConfig{
				MatchPattern: "foo",
			},
			Matched: true,
		},
		{
			Cmd: "/app/foo/bar",
			Config: &ProcCustomConfig{
				MatchPattern: "fox",
			},
			Matched: false,
		},
		{
			Cmd: "/app/foo/bar",
			Config: &ProcCustomConfig{
				MatchPattern: ".*foo",
			},
			Matched: true,
		},
		{
			Cmd: "/app/foo/bar",
			Config: &ProcCustomConfig{
				MatchPattern: "^foo$",
			},
			Matched: false,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.Config.Setup()
			assert.Equal(t, tt.Matched, tt.Config.match(tt.Cmd))
		})
	}
}
