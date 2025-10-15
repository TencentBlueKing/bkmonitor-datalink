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
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func (s *BkLogSidecar) reloadBkunifylogbeat() error {
	f, err := os.Create(config.WindowsReloadPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write([]byte("signal"))
	return err
}

func resolveContainerdPath(containerStatus *v1alpha2.ContainerStatusResponse, pid int) (string, string, error) {
	return "", containerStatus.Status.LogPath, nil
}
