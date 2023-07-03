// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows
// +build windows

package basereport

import (
	"github.com/yusufpapurcu/wmi"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// windows独有的init操作，为了可以初始化wmi依赖库的使用
// 以便防止内存泄露的问题
func init() {
	s, err := wmi.InitializeSWbemServices(wmi.DefaultClient)
	if err != nil {
		// 如果不能初始化，那么还是打印日志就好，不要让整个任务失败出问题
		// 因为发生内存泄露，总比整个采集器都凉凉了好
		logger.Errorf("failed to init SWbemServices for->[%s]", err.Error())
		return
	}
	wmi.DefaultClient.SWbemServicesClient = s
	return
}
