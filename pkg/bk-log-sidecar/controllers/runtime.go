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

import (
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

// NewRuntime new Runtime
func NewRuntime(runtimeVersion string) define.Runtime {
	// example: "docker://19.3.1", "containerd://1.4.1"
	if strings.HasPrefix(runtimeVersion, string(define.RuntimeTypeContainerd)) || strings.HasPrefix(runtimeVersion, string(define.RuntimeTypeEks)) {
		return NewContainerdRuntime()
	} else if strings.HasPrefix(runtimeVersion, string(define.RuntimeTypeDocker)) {
		return NewDockerRuntime()
	} else {
		utils.CheckError(fmt.Errorf("runtime init failed, unknown version: %s", runtimeVersion))
	}
	return nil
}
