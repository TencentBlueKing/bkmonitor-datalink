// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package overall

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/process"

	selfcheck "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/self-check"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/self-check/config"
)

func init() {
	selfcheck.RegisterTestMap("QuickTest", Check)
}

func Check() {
	ckProcess := checkPidProcess()
	if !ckProcess {
		fmt.Println("bkmonitorbeat process may not running, please check!")
	} else {
		fmt.Println("bkmonitorbeat process status is ok!")
	}
	ckSocket := checkDomainSocket()
	if !ckSocket {
		fmt.Println("unable to connect unix domain socket, please check socket file.")
	} else {
		fmt.Println("bkmonitorbeat unix domain socket status is ok!")
	}

	checkLog()
}

// CheckPidProcess 检测对应 Pid 的进程是否存在
func checkPidProcess() bool {
	var running bool
	pid := config.GetProcessPid()

	// 无法读取 pidFile 以及 pid 为空的情况
	if pid == "" {
		fmt.Println("pid is empty, unable to check bkmonitorbeat process")
		return running
	}

	// 尝试捕获特定 pid 的进程
	pid32, err := strconv.ParseInt(pid, 10, 32)
	if err != nil {
		fmt.Printf("transform string pid to int32 error:%s \n", err)
		return running
	}

	if p, err := process.NewProcess(int32(pid32)); err == nil {
		if r, err := p.IsRunning(); err == nil {
			running = r
		}
	}
	return running
}

// checkDomainSocket 检测 domain socket 是否正常
func checkDomainSocket() bool {
	var socketFlag bool
	socketPath := config.GetConfInfo().OutPut.Endpoint
	if socketPath == "" {
		fmt.Println("unable to get the path of socket file")
		return socketFlag
	}

	// 当 err != nil  的时候说明 DomainSocket 文件夹不存在或其他错误
	_, err := os.Stat(socketPath)
	if err != nil {
		fmt.Printf("unable to get socket file:%s error: %s\n", socketPath, err)
		return socketFlag
	}

	// 尝试通过 DomainSocket 文件建立连接
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Printf("unable to connect unix socket, error: %s\n", err)
		return socketFlag
	}
	// 仅仅是尝试与 unix domain socket 文件建立连接测试，不进行读写操作
	defer conn.Close()
	return true
}

// checkLog 检测 bkmonitorbeat 近期的日志情况
func checkLog() {
	logDir := config.GetConfInfo().Path.Log
	if logDir == "" {
		fmt.Println("unable to get bkmonitorbeat log path")
		return
	}

	files, err := os.ReadDir(logDir)
	// 无法读取文件夹的情况
	if err != nil {
		fmt.Printf("unable to open logDir: %s, error: %s\n", logDir, err)
		return
	}

	logs := make([]os.DirEntry, 0)
	for _, v := range files {
		// 尝试获取所有 bkmonitorbeat 的日志文件
		if strings.Contains(v.Name(), "bkmonitorbeat") {
			logs = append(logs, v)
			fmt.Printf("file name %s\n", v.Name())
			if info, err := v.Info(); err == nil {
				fmt.Printf("file time %s\n", info.ModTime())
			}
		}
	}
	// 日志文件不存在则不继续
	if len(logs) == 0 {
		fmt.Printf("logDir: %s is empty\n", logDir)
		return
	}
	// 尝试捕获最近一天的 bkmonitorbeat 以及 bkmonitorbeat.log 文件
	// cat xxx | grep "ERROR" 拉取错误的代码
	// cat xxx | grep "ERROR" | grep "cpu.go" 拉取CPU相关的错误
	// ...
	// 分批次输出错误的内容 限定行数
}
