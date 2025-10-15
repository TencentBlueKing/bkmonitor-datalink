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
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

func scanPidLine(content []byte) int {
	if len(content) == 0 {
		return -1
	}

	var pid int
	if _, err := fmt.Sscanln(string(content), &pid); err != nil {
		return -1
	}

	if pid <= 0 {
		return -1
	}
	return pid
}

func (s *BkLogSidecar) reloadBkunifylogbeat() error {
	pidContent, err := ioutil.ReadFile(config.BkunifylogbeatPidFile)
	if utils.NotNil(err) {
		s.log.Error(err, "read bkunifylogbeat pid file failed", "pid", config.BkunifylogbeatPidFile)
		return err
	}
	pid := scanPidLine(pidContent)
	if pid <= 0 {
		return fmt.Errorf("not get pid")
	}
	proc, err := os.FindProcess(pid)
	if utils.NotNil(err) {
		return fmt.Errorf("get process failed pid -> [%d]", pid)
	}
	err = proc.Signal(syscall.SIGUSR1)
	if utils.NotNil(err) {
		s.log.Error(err, "reload bkunifylogbeat failed")
		return err
	}
	s.log.Info("reload agent success")
	return nil
}

func resolveContainerdPath(containerStatus *v1alpha2.ContainerStatusResponse, pid int) (string, string, error) {
	rootPath := fmt.Sprintf("/proc/%d/root", pid)
	if pid == 0 {
		rootPath = filepath.Join(config.ContainerdStatePath, ContainerdTaskDirName, config.ContainerdNamespace, containerStatus.Status.Id, ContainerdRootFsDirName)
	}

	logPath := containerStatus.Status.LogPath

	// 如果logPath是软链，需要转换为真实路径
	realLogPath, err := define.EvalSymlinks(logPath)
	if err == nil {
		logPath = realLogPath
	}

	return rootPath, logPath, err
}
