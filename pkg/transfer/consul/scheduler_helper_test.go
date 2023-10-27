// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

func TestExtractFlowDetailed(t *testing.T) {
	cases := []struct {
		In  string
		Out *define.FlowItem
	}{
		{
			In: `bk_bkmonitorv3_enterprise_production/service/v1/default/flow/bkmonitorv3-2604497288/1500128`,
			Out: &define.FlowItem{
				DataID:  1500128,
				Cluster: "default",
				Type:    "",
				Service: "bkmonitorv3-2604497288",
				Path:    `bk_bkmonitorv3_enterprise_production/service/v1/default/flow/bkmonitorv3-2604497288/1500128`,
				Flow:    100,
			},
		},
		{
			In:  `flow/bkmonitorv3-2604497288/1500128`,
			Out: nil,
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Out, extractFlowDetailed(c.In, "100"))
	}
}
