// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryStringPattern(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
		isRegexp          bool
	}{
		{name: "通配符内的普通标点保留在同一字面片段", value: `*a.b@c#d?`, want: `*a.b@c#d?`},
		{name: "空格和字段分隔符作为字面字符", value: `*ERR code:/log*`, want: `*ERR\ code\:\/log*`},
		{name: "只有转义星号时精确匹配整条词条", value: `A\*B.x@#`, want: `/A\*B\.x\@\#/`},
		{name: "单星号查询已有词条", value: "*", want: "**"},
		{name: "末尾反斜线作为字面字符", value: `*ERR\`, want: `*ERR\\`},
		{name: "正则保留已有转义并正确引用斜线", value: `(a\/b|c/d|e\\/f)`, want: `/(a\/b|c\/d|e\\\/f)/`, isRegexp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, queryStringPattern(tc.value, tc.isRegexp))
		})
	}
}
