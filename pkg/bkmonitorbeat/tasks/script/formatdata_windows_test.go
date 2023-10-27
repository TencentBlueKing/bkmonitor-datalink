// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows
// +build windows

package script

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// FormatSuite :
type FormatSuite struct {
	suite.Suite
}

func (s *FormatSuite) TestGatherRun() {
	cases := []struct {
		origin string
		expect string
	}{
		{"%script_test%", "$script_test"},
		{"powershell -file %bk_script_name%", "powershell -file $bk_script_name"},
		{"powershell -file %bk_script_name% %bk_script_name1%", "powershell -file $bk_script_name $bk_script_name1"},
		{"echo hello", "echo hello"},
		{"C:\\gse\\download\\ps.ps1", "C:\\\\gse\\\\download\\\\ps.ps1"},
	}

	for _, c := range cases {
		s.Equal(c.expect, ShellWordPreProcess(c.origin))
	}
}

// TestFormat :
func TestFormat(t *testing.T) {
	suite.Run(t, &FormatSuite{})
}
