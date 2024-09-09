// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

func TestCalcShouldStatefulSetWorker(t *testing.T) {
	type Arg struct {
		Input  int
		Output int
	}

	args := []Arg{
		{
			Input: 100, Output: 1,
		},
		{
			Input: 200, Output: 1,
		},
		{
			Input: 300, Output: 2,
		},
		{
			Input: 1000, Output: 5,
		},
		{
			Input: 2000, Output: 10,
		},
		{
			Input: 2200, Output: 10,
		},
	}

	configs.G().StatefulSetWorkerHpa = true
	configs.G().StatefulSetMaxReplicas = 10
	configs.G().StatefulSetWorkerFactor = 200

	for _, arg := range args {
		assert.Equal(t, arg.Output, calcShouldStatefulSetWorker(arg.Input))
	}
}
