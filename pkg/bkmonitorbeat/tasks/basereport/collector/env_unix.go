// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	FileMaxPath = "/proc/sys/fs/file-max"
	FileNrPath  = "/proc/sys/fs/file-nr"
	VersionPath = "/proc/version"
	StatPath    = "/proc/stat"
	PtsPath     = "/dev/pts"
)

// cat /proc/sys/fs/file-max

func GetMaxFiles() (int, error) {
	fileContent, err := os.ReadFile(FileMaxPath)
	if err != nil {
		return 0, err
	}
	fileCount := strings.Split(string(fileContent), "\n")
	if len(fileCount) > 0 {
		return strconv.Atoi(fileCount[0])
	}
	return 0, fmt.Errorf("not found Max files in %s", string(fileContent))
}

// cat /proc/sys/fs/file-nr

func GetAllocatedFiles() (int, error) {
	fileContent, err := os.ReadFile(FileNrPath)
	if err != nil {
		return 0, err
	}

	// cat /proc/sys/fs/file-nr
	// 3264    0       3261376
	//
	// - allocated
	// - unused
	// - max
	parts := bytes.Split(bytes.TrimSpace(fileContent), []byte("\u0009"))
	if len(parts) < 3 {
		return 0, fmt.Errorf("unexpected number of file stats in (%s)", string(fileContent))
	}
	return strconv.Atoi(string(parts[0]))
}

// cat /proc/version

func GetUname() (string, error) {
	fileContent, err := os.ReadFile(VersionPath)
	if err != nil {
		// freebsd无此文件，使用系统调用实现
		if os.IsNotExist(err) {
			var uname unix.Utsname
			err = unix.Uname(&uname)
			if err != nil {
				return "", nil
			}
			fields := [][]byte{
				uname.Sysname[:], uname.Nodename[:], uname.Release[:], uname.Version[:], uname.Machine[:],
			}
			parts := make([]string, 0, len(fields))
			for _, field := range fields {
				parts = append(parts, string(field))
			}
			s := strings.Join(parts, " ")
			return s, nil
		}
		return "", nil
	}
	return string(fileContent), nil
}

// GetLoginUsers: 获取当前登录的用户数量，通过遍历/dev/pts下的终端数量，判断用户个数
// 返回内容是map，key为用户名，value为该用户登录的数量，以便统计
func GetLoginUsers() (int, error) {
	var (
		result       int
		fileInfoList []os.DirEntry
		err          error
	)

	if _, err := os.Stat(PtsPath); err != nil && os.IsNotExist(err) {
		return 0, fmt.Errorf("cannot get login user info for paht->[%s] is not exists", PtsPath)
	}

	// 遍历/dev/pts下的所有内容
	if fileInfoList, err = os.ReadDir(PtsPath); err != nil {
		return 0, errors.Wrapf(err, "failed to get PtsPath files")
	}

	for _, fileInfo := range fileInfoList {
		if _, err = strconv.Atoi(fileInfo.Name()); err != nil {
			logger.Warnf("bad tty id->[%s] will jump it.", fileInfo.Name())
			continue
		}
		result++
	}

	return result, nil
}

func GetProcEnv() (runningProc, blockedProc, proc, ctxt int, lasterr error) {
	fileContent, err := os.ReadFile(StatPath)
	if err != nil {
		// freebsd无此文件，忽略报错
		if os.IsNotExist(err) {
			return 0, 0, 0, 0, nil
		}
		return 0, 0, 0, 0, err
	}

	runningProc, err = regexValue("procs_running", fileContent)
	blockedProc, err = regexValue("procs_blocked", fileContent)
	proc, err = regexValue("processes", fileContent)
	ctxt, err = regexValue("ctxt", fileContent)
	return runningProc, blockedProc, proc, ctxt, err
}

func regexValue(name string, content []byte) (int, error) {
	expr := name + "\\s+[0-9]+"
	reg, err := regexp.Compile(expr)
	var line []byte
	if err == nil {
		line = reg.Find(content)
	} else {
		logger.Errorf("Compile regex failed %s", err)
		return 0, err
	}
	value := strings.Split(string(line), " ")
	return strconv.Atoi(value[len(value)-1])
}
