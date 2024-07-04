// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"github.com/gin-gonic/gin"
)

// NewHTTPService new a http service
func NewHTTPService() *gin.Engine {
	svr := NewProfHttpService()
	addMetricMiddleware(svr)

	// 路由配置
	bmwRouter := svr.Group(RouterPrefix)
	taskRouter := bmwRouter.Group(TaskRouterPrefix)
	{
		taskRouter.GET("", ListTask)
		taskRouter.POST("", CreateTask)
		taskRouter.DELETE("", RemoveTask)
		taskRouter.DELETE(DeleteAllTaskPath, RemoveAllTask)
		taskRouter.POST(DaemonTaskReloadPath, ReloadDaemonTask)
	}
	// 动态设置日志级别
	bmwRouter.POST(SetLogLevelPath, SetLogLevel)

	return svr
}
