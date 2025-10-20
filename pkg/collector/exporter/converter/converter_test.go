// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestCleanAttributesMap(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		attrs := map[string]any{
			"":    "1",
			"foo": "bar",
		}

		attrs = CleanAttributesMap(attrs)
		assert.Len(t, attrs, 1)
		assert.Equal(t, "bar", attrs["foo"])
	})

	t.Run("Trim", func(t *testing.T) {
		attrs := map[string]any{
			" ":   "1",
			"foo": "bar",
		}

		attrs = CleanAttributesMap(attrs)
		assert.Len(t, attrs, 1)
		assert.Equal(t, "bar", attrs["foo"])
	})
}

func TestCommonConverter(t *testing.T) {
	conv := NewCommonConverter(&Config{})
	defer conv.Clean()

	conv.Convert(&define.Record{
		RecordType: define.RecordLogPush,
		Data: &define.LogPushData{
			Data: []string{"hello", "world"},
		},
	}, func(events ...define.Event) {})
}
