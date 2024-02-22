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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestGetMustVmQueryFeatureFlag(t *testing.T) {
	ctx := context.Background()

	log.InitTestLogger()
	InitMetadata()

	featureFlag.MockFeatureFlag(ctx, `{
	  	"must-vm-query": {
	  		"variations": {
	  			"Default": false,
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [{
	  			"query": "tableID in [\"table_id_1\", \"table_id_2\"] and spaceUid in [\"space_uid\"]",
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
	  			"query": "tableID in [\"table_id_1\"]",
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
		SpaceUid string
		TableID  string

		Start int64
		End   int64

		Expected bool
	}{
		"vm 查询，时间区间不符合配置中的时间 - 1": {
			SpaceUid: "space_uid",
			TableID:  "table_id_1",
			Start:    10000,
			End:      20000,
			Expected: false,
		},
		"vm 查询，时间区间不符合配置中的时间 - 2": {
			SpaceUid: "space_uid",
			TableID:  "table_id_1",
			Start:    30000,
			End:      40000,
			Expected: false,
		},
		"vm 查询，时间区间符合配置中的时间": {
			SpaceUid: "space_uid",
			TableID:  "table_id_1",
			Start:    30001,
			End:      40000,
			Expected: true,
		},
		"vm 未配置时间限制": {
			SpaceUid: "space_uid",
			TableID:  "table_id_2",
			Start:    10000,
			End:      20000,
			Expected: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
			SetUser(ctx, "", c.SpaceUid, "")
			SetQueryParams(ctx, &QueryParams{
				Start: c.Start,
				End:   c.End,
			})

			actual := GetMustVmQueryFeatureFlag(ctx, c.TableID)
			assert.Equal(t, c.Expected, actual)
		})
	}

}
