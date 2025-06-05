// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestFieldFormat_EncodeAndDecode(t *testing.T) {
	testCase := map[string]struct {
		q        []string
		expected []string
	}{
		"q-1": {
			q: []string{
				"__ext.container",
				"__ext.cloud.tencent.com/asset-code",
			},
			expected: []string{
				"__ext__bk_46__container",
				"__ext__bk_46__cloud__bk_46__tencent__bk_46__com__bk_47__asset__bk_45__code",
			},
		},
		"q-2": {
			q: []string{
				"__ext*container$",
				"!_ext...",
			},
			expected: []string{
				"__ext__bk_42__container__bk_36__",
				"__bk_33___ext__bk_46____bk_46____bk_46__",
			},
		},
		// 命中没有配置别名的 tableID
		"q-4": {
			q: []string{
				"ext_container",
			},
			expected: []string{
				"ext_container",
			},
		},
	}

	InitMetadata()
	ctx := InitHashID(context.Background())
	log.InitTestLogger()

	for name, c := range testCase {
		t.Run(name, func(t *testing.T) {
			ctx = InitHashID(ctx)

			pdf := GetFieldFormat(ctx)

			assert.Equal(t, len(c.expected), len(c.q))

			for idx, q := range c.q {

				r := pdf.EncodeFunc()(q)

				log.Infof(ctx, "encode: %s => %s", q, r)

				if len(c.expected) == len(c.q) {
					assert.Equal(t, c.expected[idx], r)
				}

				// 别名转换无需还原
				nr := pdf.DecodeFunc()(r)
				log.Infof(ctx, "decode: %s => %s", r, nr)

				assert.Equal(t, q, nr)

			}
		})
	}
}
