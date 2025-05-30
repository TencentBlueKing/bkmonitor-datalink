// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package user_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/user"
)

func TestMain(m *testing.M) {
	// cfg.FilePath = "../../../bmw_test.yaml"
	// cfg.InitConfig()

	// m.Run()
}

func TestListTenant(t *testing.T) {
	userApi, err := api.GetUserApi("system")
	if err != nil {
		t.Errorf("TestListTenant failed, err: %v", err)
		return
	}

	var result user.ListTenantResp
	_, err = userApi.ListTenant().SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestListTenant failed, err: %v", err)
		return
	}
	t.Logf("TestListTenant success, result: %v", result)
}
