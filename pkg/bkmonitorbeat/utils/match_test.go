// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

func TestWildcardToRegex(t *testing.T) {
	cases := []struct{ in, out string }{
		{`?`, `.{1}`},
		{`*`, `.*`},
		{`a.b`, `a\.b`},
		{`simba.*`, `simba\..*`},
		{`simba.py?`, `simba\.py.{1}`},
		{`http?://*.qq.com`, `http.{1}://.*\.qq\.com`},
	}

	for _, c := range cases {
		result := utils.WildcardToRegex(c.in)
		assert.Equal(t, c.out, result, "convert %v except %v but got %v", c.in, result, c.out)
	}
}

// TestMatchSuite :
type TestMatchSuite struct {
	suite.Suite
}

// TestMatch :
func TestMatch(t *testing.T) {
	suite.Run(t, &TestMatchSuite{})
}

func (s *TestMatchSuite) TestIsMatchString() {
	cases := []struct {
		method, content, except string
		pass                    bool
	}{
		{"", ``, ``, true},

		{utils.MatchRegex, `123`, `\d+`, true},
		{utils.MatchRegex, `{"result":true}`, `result.*`, true},
		{utils.MatchRegex, `ok`, `^ok$`, true},
		{utils.MatchRegex, `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaax`, `^(a+)+$`, false},
		{utils.MatchRegex, `I am fine`, `fine`, true},

		{utils.MatchWildcard, `123`, `1?3`, true},
		{utils.MatchWildcard, `123`, `1??`, true},
		{utils.MatchWildcard, `123`, `1*`, true},
		{utils.MatchWildcard, `123`, `*23`, true},
		{utils.MatchWildcard, `123`, `1*3`, true},
		{utils.MatchWildcard, `1223`, `1*3`, true},
		{utils.MatchWildcard, `1`, `1*`, true},
		{utils.MatchWildcard, `23`, `*23`, true},
		{utils.MatchWildcard, `13`, `1*3`, true},
		{utils.MatchWildcard, `1234`, `1*3`, true},

		{utils.MatchWildcard, `122`, `1?3`, false},
		{utils.MatchWildcard, `12`, `1??`, false},
	}

	for i, c := range cases {
		s.Run(fmt.Sprintf("i-%d-method-%s", i, c.method), func() {
			result := utils.IsMatchString(c.method, c.content, c.except)
			s.Equal(c.pass, result, "test content[%v] with %v[%v] fail as string", c.content, c.method, c.except)

			result = utils.IsMatch(c.method, []byte(c.content), []byte(c.except))
			s.Equal(c.pass, result, "test content[%v] with %v[%v] fail", c.content, c.method, c.except)
		})
	}
}

func (s *TestMatchSuite) TestIsMatch() {
	cases := []struct {
		method          string
		content, except []byte
		pass            bool
	}{
		{utils.MatchContains, []byte("believe"), []byte("lie"), true},
		{utils.MatchContains, []byte("I am fine"), []byte(""), true},

		{utils.MatchNotContains, []byte("I am fine"), []byte("ok"), true},
		{utils.MatchNotContains, []byte("I am fine"), []byte(""), true},

		{utils.MatchEqual, []byte("ok"), []byte("ok"), true},
		{utils.MatchEqual, []byte(""), []byte(""), true},

		{utils.MatchNotEqual, []byte("ok"), []byte("fail"), true},
		{utils.MatchNotEqual, []byte(""), []byte(""), true},

		{utils.MatchStartsWith, []byte("abcd"), []byte("a"), true},
		{utils.MatchStartsWith, []byte("abcd"), []byte("ab"), true},
		{utils.MatchStartsWith, []byte("abcd"), []byte("bc"), false},

		{utils.MatchNotStartsWith, []byte("abcd"), []byte("bc"), true},
		{utils.MatchNotStartsWith, []byte("abcd"), []byte("ab"), false},

		{utils.MatchEndsWith, []byte("abcd"), []byte("cd"), true},
		{utils.MatchEndsWith, []byte("abcd"), []byte("bc"), false},

		{utils.MatchNotEndsWith, []byte("abcd"), []byte("cd"), false},
		{utils.MatchNotEndsWith, []byte("abcd"), []byte("bc"), true},

		{utils.MatchHex, []byte("yakov"), []byte("79616b6f76"), true},
		{utils.MatchHex, []byte("yakovx"), []byte("79616b6f76"), false},
	}

	for i, c := range cases {
		s.Run(fmt.Sprintf("i-%d-method-%s", i, c.method), func() {
			result := utils.IsMatch(c.method, c.content, c.except)
			s.Equal(c.pass, result, "test content[%v] with %v[%v] fail", string(c.content), c.method, string(c.except))
		})
	}
}
