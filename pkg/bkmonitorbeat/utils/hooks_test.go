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
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

func TestHookManager(t *testing.T) {
	var manager utils.HookManager
	called := 0

	manager.Add(func(ctx context.Context) {
		called += 11
	})
	manager.Add(func(ctx context.Context) {
		called += 7
	})

	manager.Apply(context.TODO())

	if called != 18 {
		t.Errorf("hook manager apply error")
	}
}
