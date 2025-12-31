// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestGetBkDataTableIDCheck(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	metadata.InitMetadata()

	MockFeatureFlag(ctx, `{
    "bk-data-table-id-auth": {
        "variations": {
            "Default": true,
            "true": true,
            "false": false
        },
        "targeting": [
            {
                "query": "spaceUid in [\"bkdata\", \"bkdata_1\"]",
                "percentage": {
                    "false": 100,
					"true": 0
                }
            },
			{
                "query": "tableID sw \"10000_\"",
                "percentage": {
                    "false": 100,
					"true": 0
                }
            }
        ],
        "defaultRule": {
            "variation": "Default"
        }
    }
}`)

	var actual bool

	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkdata_1"})
	actual = GetBkDataTableIDCheck(ctx, "")
	assert.Equal(t, false, actual)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkmonitor"})
	actual = GetBkDataTableIDCheck(ctx, "")
	assert.Equal(t, true, actual)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkmonitor"})
	actual = GetBkDataTableIDCheck(ctx, "10000_demo")
	assert.Equal(t, false, actual)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkdata_1_1"})
	actual = GetBkDataTableIDCheck(ctx, "")
	assert.Equal(t, true, actual)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkdata"})
	actual = GetBkDataTableIDCheck(ctx, "")
	assert.Equal(t, false, actual)
}

func TestGetMustVmQueryFeatureFlag(t *testing.T) {
	ctx := context.Background()

	log.InitTestLogger()
	metadata.InitMetadata()

	MockFeatureFlag(ctx, `{
	  	"must-vm-query": {
	  		"variations": {
	  			"Default": false,
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [{
	  			"query": "tableID in [\"table_id_1\", \"table_id_2\"]",
	  			"percentage": {
	  				"true": 100,
	  				"false":0 
	  			}
	  		}],
	  		"defaultRule": {
	  			"variation": "Default"
	  		}
	  	},
		"range-vm-query": {
			"variations": {
	  			"Default": 0,
	  			"true": 30000
	  		},
			"targeting": [{
	  			"query": "tableID in [\"table_id_1\", \"table_id_3\"]",
	  			"percentage": {
	  				"true": 100
	  			}
	  		}],
	  		"defaultRule": {
	  			"variation": "Default"
	  		}
		}
	  }`)

	for name, c := range map[string]struct {
		TableID string

		Start int64
		End   int64

		Expected bool
	}{
		"vm 查询，时间区间不符合配置中的时间 - 1": {
			TableID:  "table_id_1",
			Start:    10000,
			End:      20000,
			Expected: false,
		},
		"vm 查询，时间区间不符合配置中的时间 - 2": {
			TableID:  "table_id_1",
			Start:    30000,
			End:      40000,
			Expected: false,
		},
		"vm 查询，时间区间符合配置中的时间": {
			TableID:  "table_id_1",
			Start:    30001,
			End:      40000,
			Expected: true,
		},
		"vm 未配置时间限制": {
			TableID:  "table_id_2",
			Start:    10000,
			End:      20000,
			Expected: true,
		},
		"vm 查询，不符合时间区间配置中的时间，但是不在 must-vm-query 中": {
			TableID:  "table_id_3",
			Start:    30000,
			End:      40000,
			Expected: false,
		},
		"vm 查询，时间区间符合配置中的时间，但是不在 must-vm-query 中": {
			TableID:  "table_id_3",
			Start:    30001,
			End:      40000,
			Expected: true,
		},
		"未配置 任何 vm 查询": {
			TableID:  "table_id_4",
			Start:    30001,
			End:      40000,
			Expected: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()

			start, end := time.Unix(c.Start, 0), time.Unix(c.End, 0)
			metadata.GetQueryParams(ctx).SetTime(start, start, end, 0, "", "")

			actual := GetMustVmQueryFeatureFlag(ctx, c.TableID)
			assert.Equal(t, c.Expected, actual)
		})
	}
}

func TestGetVmRtFeatureFlag(t *testing.T) {
	ctx := context.Background()

	log.InitTestLogger()
	metadata.InitMetadata()
	MockFeatureFlag(ctx, `{
		"exclusion-vm-rt": {
			"variations": {
	  			"Default": false,
	  			"true": true,
	  			"false": false
	  		},
			"targeting": [{
	  			"query": "tableID in [\"vm\", \"vm_1\", \"vm_2\", \"vm_3\", \"vm_4\"]",
	  			"variation": "true"
	  		}],
	  		"defaultRule": {
	  			"variation": "Default"
	  		}
		}
	}`)

	for name, c := range map[string]struct {
		TableID  string
		Expected bool
	}{
		"排除VM结果表查询 - vm表": {
			TableID:  "vm",
			Expected: true,
		},
		"排除VM结果表查询 - vm_1表": {
			TableID:  "vm_1",
			Expected: true,
		},
		"排除VM结果表查询 - vm_2表": {
			TableID:  "vm_2",
			Expected: true,
		},
		"排除VM结果表查询 - vm_3表": {
			TableID:  "vm_3",
			Expected: true,
		},
		"排除VM结果表查询 - vm_4表": {
			TableID:  "vm_4",
			Expected: true,
		},
		"不排除非VM结果表查询 - 普通表": {
			TableID:  "normal_table",
			Expected: false,
		},
		"不排除非VM结果表查询 - 其他表": {
			TableID:  "other_table",
			Expected: false,
		},
		"不排除非VM结果表查询 - 空表ID": {
			TableID:  "",
			Expected: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()

			// gzl 设置用户信息
			metadata.SetUser(ctx, &metadata.User{
				HashID:   "test_user",
				Name:     "测试用户",
				Source:   "test",
				SpaceUID: "test_space",
			})

			// gzl 调用GetVmRtFeatureFlag方法进行测试
			actual := GetVmRtFeatureFlag(ctx, c.TableID)
			assert.Equal(t, c.Expected, actual, "表ID: %s, 期望排除状态: %v", c.TableID, c.Expected)
		})
	}
}
