// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

type TestGetValueByNameS struct {
	Value int64
}

func TestGetValueByName(t *testing.T) {
	x := TestGetValueByNameS{
		Value: 1,
	}
	i := x

	v1, ok := utils.GetValueByName(x, "Value")
	if v1.Int() != x.Value || !ok {
		t.Errorf("get value error: %v", v1)
	}

	v2, ok := utils.GetValueByName(x, "Nothing")
	if v2.IsValid() || ok {
		t.Errorf("get value error: %v", v2)
	}

	v3, ok := utils.GetValueByName(i, "Value")
	if v3.Int() != x.Value || !ok {
		t.Errorf("get value error: %v", v3)
	}

	v4, ok := utils.GetValueByName(i, "Nothing")
	if v4.IsValid() || ok {
		t.Errorf("get value error: %v", v4)
	}
}
