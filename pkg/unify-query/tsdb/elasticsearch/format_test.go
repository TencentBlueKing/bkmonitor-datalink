// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestFormatFactory_RangeQuery(t *testing.T) {
	for name, c := range map[string]struct {
		start     int64
		end       int64
		timeField metadata.TimeField
		expected  string
	}{
		"test-1": {
			start: 0,
			end:   10000000,
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeTime,
				Unit: Millisecond,
			},
			expected: ``,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).WithQuery("", c.timeField, c.start, c.end, "", 0, 0)
			res, err := fact.RangeQuery().Source()
			if err == nil {
				resJson, _ := json.Marshal(res)
				assert.Equal(t, c.expected, string(resJson))
			}
		})
	}
}
