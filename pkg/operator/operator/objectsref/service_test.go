// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchLabels(t *testing.T) {
	t.Run("Match/Less", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
			"k3": "v3",
		}

		assert.True(t, matchLabels(subset, set))
	})

	t.Run("Match/Equal", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}

		assert.True(t, matchLabels(subset, set))
	})

	t.Run("Match/Greater", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
			"k3": "v3",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}

		assert.False(t, matchLabels(subset, set))
	})
}
