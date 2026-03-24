// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/host"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// tasklist
func GetProcs() (int, error) {
	cmd := exec.Command("cmd", "/C", "tasklist")
	std, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		logger.Errorf("cmd Start err:%s", err)
		return 0, nil
	}
	content, err := io.ReadAll(std)
	if err != nil {
		logger.Errorf("read content err :%s", err)
		return 0, nil
	}
	logger.Debugf("get proclist by tasklist %s", string(content))
	if err := cmd.Wait(); err != nil {
		logger.Errorf("cmd Start err:%s", err)
		return 0, nil
	}
	procList := strings.Split(string(content), "\r\n")
	procCount := len(procList)
	if procCount >= 4 {
		return len(procList) - 4, nil
	}
	return 0, fmt.Errorf("get procCount from %v failed", procList)
}

func GetMaxFiles() (int, error) {
	cmd := exec.Command("REG", "QUERY", "HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Windows", "/v", "GDIProcessHandleQuota")
	logger.Debug("exec command: REG QUERY \"HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Windows\" /v GDIProcessHandleQuota", "")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Errorf("can not obtain stdout pipe for command:%s", err)
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		logger.Errorf("The command is err : %s", err)
		return 0, err
	}
	bytes, err := io.ReadAll(stdout)
	res := string(bytes)
	if err != nil {
		logger.Errorf("ReadAll Stdout:%s", err)
		return 0, err
	}
	if err := cmd.Wait(); err != nil {
		logger.Errorf("wait err:%s", err)
	}
	reslist := strings.Split(res, "\r\n")
	var decimal uint64
	for _, line := range reslist {
		if !strings.Contains(line, "GDIProcessHandleQuota") {
			continue
		}
		if ind := strings.Index(line, "0x"); ind != -1 {
			hexres := line[ind+2:]
			logger.Debugf("hex: %s", hexres)
			if decimal, err = strconv.ParseUint(hexres, 16, 32); err != nil {
				logger.Errorf("Transcoding hex to decimal failed：%s", err)
				return 0, err
			}
		}
	}
	return int(decimal), nil
}

// GetAllocatedFiles windows 系统下无对应的实现
func GetAllocatedFiles() (int, error) {
	return 0, nil
}

func GetUname() (string, error) {
	infoStat, err := host.Info()
	if err != nil {
		return "", err
	}
	return infoStat.KernelVersion, nil
}

func GetLoginUsers() (int, error) {
	cmd := exec.Command("query", "user")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Errorf("Error:can not obtain stdout pipe for command:%s", err)
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		logger.Errorf("Error:The command is err :%s", err)
		return 0, err
	}
	bytes, err := io.ReadAll(stdout)
	if err != nil {
		logger.Errorf("ReadAll Stdout:%s", err)
		return 0, err
	}
	// 除去开头和结尾的两行
	if userlist := strings.Split(string(bytes), "\r\n"); len(userlist) >= 2 {
		return len(userlist) - 2, nil
	}
	return 0, fmt.Errorf("get userlist error %s", string(bytes))
}

func GetProcEnv() (runningProc, blockedProc, proc, ctxt int, lasterr error) {
	runningProc, err := GetProcs()
	return 0, 0, 0, 0, err
}
