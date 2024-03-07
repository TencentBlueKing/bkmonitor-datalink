// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows

package collector

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

func GetMaxFiles() (int, error) {
	cmd := exec.Command("REG", "QUERY", "HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Windows", "/v", "GDIProcessHandleQuota")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Run(); err != nil {
		return 0, err
	}

	bs, err := io.ReadAll(stdout)
	if err != nil {
		return 0, err
	}

	var decimal uint64
	content := string(bs)
	for _, line := range strings.Split(content, "\r\n") {
		if !strings.Contains(line, "GDIProcessHandleQuota") {
			continue
		}
		if idx := strings.Index(line, "0x"); idx != -1 {
			hex := line[idx+2:]
			if decimal, err = strconv.ParseUint(hex, 16, 32); err != nil {
				return 0, errors.Wrapf(err, "valid decimal line: %s", line)
			}
		}
	}
	return int(decimal), nil
}

func GetLoginUsers() (int, error) {
	cmd := exec.Command("query", "user")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Run(); err != nil {
		return 0, err
	}

	bs, err := io.ReadAll(stdout)
	if err != nil {
		return 0, err
	}
	// 除去开头和结尾的两行
	if users := strings.Split(string(bs), "\r\n"); len(users) >= 2 {
		return len(users) - 2, nil
	}
	return 0, fmt.Errorf("invalid users '%s'", string(bs))
}

func GetProcEnv() (runningProc, blockedProc, totalProc, ctxtProc int, lasterr error) {
	return 0, 0, 0, 0, nil
}
