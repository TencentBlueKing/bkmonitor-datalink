// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestDropEvaluator(t *testing.T) {
	t.Run("Disabled", func(t *testing.T) {
		evaluator := New(Config{
			Type: evaluatorTypeDrop,
		})
		record := &define.Record{
			RecordType: define.RecordProfiles,
			Data:       []byte("profiles"),
		}
		assert.NoError(t, evaluator.Evaluate(record))
	})

	t.Run("Enabled", func(t *testing.T) {
		evaluator := New(Config{
			Type:    evaluatorTypeDrop,
			Enabled: true,
		})
		record := &define.Record{
			RecordType: define.RecordProfiles,
			Data:       []byte("profiles"),
		}
		assert.Equal(t, define.ErrSkipEmptyRecord, evaluator.Evaluate(record))
	})
}
