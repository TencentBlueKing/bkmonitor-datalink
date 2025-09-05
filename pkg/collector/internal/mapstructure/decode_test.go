// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapstructure

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDecode(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		input := map[string]any{
			"int":      1,
			"float64":  1.0,
			"string":   "foo",
			"duration": "10s",
		}

		type Output struct {
			Int      int           `mapstructure:"int"`
			Float    float64       `mapstructure:"float64"`
			String   string        `mapstructure:"string"`
			Duration time.Duration `mapstructure:"duration"`
		}

		var output Output
		assert.NoError(t, Decode(input, &output))
		assert.Equal(t, Output{
			Int:      1,
			Float:    1.0,
			String:   "foo",
			Duration: time.Second * 10,
		}, output)
	})

	t.Run("Failed", func(t *testing.T) {
		input := "foo"

		type Output struct {
			Foo int `mapstructure:"foo"`
		}

		var output Output
		assert.Error(t, Decode(input, &output))
	})
}
