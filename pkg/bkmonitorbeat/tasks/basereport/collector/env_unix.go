// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	pathFsFileMax = "/proc/sys/fs/file-max"
	pathProcStat  = "/proc/stat"
	pathDevPts    = "/dev/pts"
)

// GetMaxFiles 获取 maxfiles 数值
func GetMaxFiles() (int, error) {
	content, err := os.ReadFile(pathFsFileMax)
	if err != nil {
		return 0, err
	}

	parts := strings.Split(string(content), "\n")
	if len(parts) > 0 {
		return strconv.Atoi(parts[0])
	}
	return 0, fmt.Errorf("not found maxfile in %s", string(content))
}

// GetLoginUsers 获取当前登录的用户数量，通过遍历 /dev/pts 下的终端数量，判断用户个数
func GetLoginUsers() (int, error) {
	entries, err := os.ReadDir(pathDevPts)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read PtsPath files")
	}

	count := 0
	for _, entry := range entries {
		if _, err = strconv.Atoi(entry.Name()); err != nil {
			logger.Warnf("ignore invalid tty id: %s", entry.Name())
			continue
		}
		count++
	}
	return count, nil
}

// GetProcEnv 获取 procenv 信息
func GetProcEnv() (runningProc, blockedProc, totalProc, ctxtProc int, lasterr error) {
	content, err := os.ReadFile(pathProcStat)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	runningProc, _ = parseProcsValue("procs_running", content)
	blockedProc, _ = parseProcsValue("procs_blocked", content)
	totalProc, _ = parseProcsValue("processes", content)
	ctxtProc, _ = parseProcsValue("ctxt", content)
	return runningProc, blockedProc, totalProc, ctxtProc, nil
}

func parseProcsValue(name string, content []byte) (int, error) {
	expr := name + "\\s+[0-9]+"
	re, err := regexp.Compile(expr)
	if err != nil {
		return 0, err
	}

	line := re.Find(content)
	parts := strings.Split(string(line), " ")
	return strconv.Atoi(parts[len(parts)-1])
}
