// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

type convergenceTrigger string

const (
	convergenceTriggerDirect               convergenceTrigger = "direct_apply"
	convergenceTriggerStartup              convergenceTrigger = "startup"
	convergenceTriggerRuntimeReconnect     convergenceTrigger = "runtime_reconnect"
	convergenceTriggerBkLogConfigReconcile convergenceTrigger = "bklogconfig_reconcile"
	convergenceTriggerPeriodicReconcile    convergenceTrigger = "periodic_reconcile"
	convergenceTriggerContainerCreate      convergenceTrigger = "container_create"
	convergenceTriggerContainerCleanup     convergenceTrigger = "container_cleanup"

	convergenceResultSuccess = "success"
	convergenceResultFailure = "failure"
)

func runtimeSubscriptionTrigger(initial bool) convergenceTrigger {
	if initial {
		return convergenceTriggerStartup
	}
	return convergenceTriggerRuntimeReconnect
}

// trigger 只从已有的生成选项推导日志触发源，不参与任何配置生成或恢复决策。
// 保持这层只读映射，可以避免为了可观测性再引入一套并行状态机。
func (o configGenerationOptions) trigger() convergenceTrigger {
	switch {
	case o.forceReload:
		return convergenceTriggerStartup
	case o.refreshDiscoveredState:
		return convergenceTriggerPeriodicReconcile
	case o.reconcile != nil:
		return convergenceTriggerBkLogConfigReconcile
	default:
		return convergenceTriggerRuntimeReconnect
	}
}
