// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToPromFormat(t *testing.T) {
	t.Run("Unordered", func(t *testing.T) {
		labels := map[string]string{
			"biz":     "foo",
			"creator": "admin",
			"zone":    "gz",
		}

		s1 := toPromFormat(labels)
		assert.Equal(t, `bkm_sli{biz="foo",creator="admin",zone="gz"} 1`, s1)

		s2 := toPromFormat(labels)
		assert.Equal(t, s1, s2)
	})

	t.Run("NoLabels", func(t *testing.T) {
		s1 := toPromFormat(nil)
		assert.Equal(t, `bkm_sli{} 1`, s1)
	})
}
