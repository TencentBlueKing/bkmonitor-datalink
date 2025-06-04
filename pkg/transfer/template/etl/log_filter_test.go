// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func TestLogFilter(t *testing.T) {
	ctx := context.Background()
	ctx = config.PipelineConfigIntoContext(ctx, &config.PipelineConfig{
		Option: map[string]interface{}{
			"log_cluster_config": map[string]interface{}{
				"log_filter": []*utils.MatchRule{
					{
						Key:       "dimensions.k1",
						Value:     []string{"v1"},
						Method:    "eq",
						Condition: "or",
					},
					{
						Key:       "dimensions.k2",
						Value:     []string{"v2"},
						Method:    "eq",
						Condition: "and",
					},
				},
			},
		},
	})

	p, err := NewLogFilter(ctx, "log_filter")
	if err != nil {
		panic(err)
	}

	matched := utils.IsRulesMatch(p.rules, map[string]interface{}{
		"dimensions": map[string]interface{}{
			"k1": "v1",
			"k2": "v2",
		},
		"metrics": map[string]interface{}{
			"log": "mylog",
		},
	})
	assert.True(t, matched)
}
