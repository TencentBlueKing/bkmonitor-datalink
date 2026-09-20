// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

// preserveRuntimeTailFiles 保留同一容器、同一 BkLogConfig 生命周期已经生效的
// tail_files 决策。这是 sidecar 计算的运行时字段，不应被周期全量因
// “容器现在已存在”而从 false 改回 true。ExtOptions 仍在序列化时保持
// 最高优先级，这里不改变用户显式配置的覆盖语义。
func (s *BkLogSidecar) preserveRuntimeTailFiles(logConfig define.LogConfigType) {
	cachedValue, ok := s.actualBkLogConfigCache.Load(logConfig.ConfigName())
	if !ok {
		return
	}
	cached := cachedValue.(define.LogConfigType)
	cachedSource, cachedTailFiles, ok := containerLogConfigRuntimeState(cached)
	if !ok {
		return
	}
	currentSource, _, ok := containerLogConfigRuntimeState(logConfig)
	if !ok || !sameBkLogConfigLifecycle(cachedSource, currentSource) {
		return
	}
	setContainerLogConfigTailFiles(logConfig, cachedTailFiles)
}

func containerLogConfigRuntimeState(
	logConfig define.LogConfigType,
) (*bluekingv1alpha1.BkLogConfig, bool, bool) {
	switch typed := logConfig.(type) {
	case *define.StdOutLogConfig:
		return &typed.BkLogConfig, typed.BkLogConfig.Spec.TailFiles, true
	case *define.ContainerLogConfig:
		return &typed.BkLogConfig, typed.BkLogConfig.Spec.TailFiles, true
	default:
		return nil, false, false
	}
}

func sameBkLogConfigLifecycle(
	cached, current *bluekingv1alpha1.BkLogConfig,
) bool {
	if cached == nil || current == nil {
		return false
	}
	if cached.UID != "" || current.UID != "" {
		return cached.UID != "" && current.UID != "" && cached.UID == current.UID
	}
	// API Server 上的对象一定有 UID；名字回退仅用于单测替身和历史
	// 内存对象，不会把生产中删除后同名重建的 CR 判为同一生命周期。
	return cached.Namespace == current.Namespace && cached.Name == current.Name
}

func setContainerLogConfigTailFiles(logConfig define.LogConfigType, tailFiles bool) {
	switch typed := logConfig.(type) {
	case *define.StdOutLogConfig:
		typed.BkLogConfig.Spec.TailFiles = tailFiles
	case *define.ContainerLogConfig:
		typed.BkLogConfig.Spec.TailFiles = tailFiles
	}
}
