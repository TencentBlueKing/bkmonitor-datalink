// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package test

import (
	"context"
	"os"
	"path"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
)

var hostIDFilePath = path.Join(os.TempDir(), "test.config")

func MakeWatcher() {
	str := `{
			"bk_host_id": 3,
			"bk_host_name": "",
			"bk_supplier_id": 0,
			"bk_supplier_account": "",
			"bk_cloud_id": 0,
			"bk_cloud_name": "default area",
			"bk_host_innerip": "127.0.0.1",
			"bk_host_outerip": "",
			"bk_os_type": "",
			"bk_os_name": "",
			"bk_mem": 0,
			"bk_cpu": 0,
			"bk_disk": 0,
			"associations": {
				"56": {
					"bk_biz_id": 3,
					"bk_biz_name": "test",
					"bk_set_id": 11,
					"bk_set_name": "aa",
					"bk_module_id": 56,
					"bk_module_name": "",
					"bk_service_status": "1",
					"bk_set_env": "3",
					"layer" : {
						"bk_inst_id" : 2,
						"bk_inst_name" : "test1",
						"bk_obj_id" : "test",
						"child" : {
							"bk_inst_id" : 31,
							"bk_inst_name" : "NEK_TEST",
							"bk_obj_id" : "nek_test",
							"child" : null
						}
					}
				},
				"82": {
					"bk_biz_id": 13,
					"layer": null,
					"bk_service_status": "1",
					"bk_module_id": 82,
					"bk_set_env": "3",
					"bk_module_name": "空闲机",
					"bk_set_name": "空闲机池",
					"bk_set_id": 22,
					"bk_biz_name": "somebody"
				}
			},
			"process": [{
				"bk_process_id": 45,
				"bk_process_name": "aa",
				"bind_ip": "",
				"port": "",
				"protocol": "1",
				"bk_func_id": "",
				"bk_func_name": "aa",
				"bk_start_param_regex": "",
				"bind_modules": [
					56
				]
			}]
		}`

	f, _ := os.Create(hostIDFilePath)
	_, _ = f.WriteString(str)
	_ = f.Close()

	w := host.NewWatcher(context.Background(), host.Config{
		HostIDPath:         hostIDFilePath,
		CMDBLevelMaxLength: 10,
		IgnoreCmdbLevel:    false,
		MustHostIDExist:    false,
	})
	define.GlobalWatcher = w
	_ = define.GlobalWatcher.Start()
	time.Sleep(1 * time.Millisecond)
}

func CleanWatcher() {
	define.GlobalWatcher.Stop()
	_ = os.Remove(hostIDFilePath)
}
