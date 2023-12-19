// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/example"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/processor"
)

type Task struct {
	Handler processor.HandlerFunc
}

var (
	exampleTask               = "async:test_example"
	CreateEsStorageIndex      = "async:create_es_storage_index"
	PushAndPublishSpaceRouter = "async:push_and_publish_space_router"
	PushSpaceToRedis          = "async:push_space_to_redis"
	AccessBkdataVm            = "async:access_bkdata_vm"

	asyncTaskDefine = map[string]Task{
		exampleTask: {
			Handler: example.HandleExampleTask,
		},
		CreateEsStorageIndex: {
			Handler: task.CreateEsStorageIndex,
		},
		PushAndPublishSpaceRouter: {
			Handler: task.PushAndPublishSpaceRouter,
		},
		PushSpaceToRedis: {
			Handler: task.PushSpaceToRedis,
		},
		AccessBkdataVm: {
			Handler: task.AccessBkdataVm,
		},
	}
)

func GetAsyncTaskMapping() map[string]Task {
	return asyncTaskDefine
}
