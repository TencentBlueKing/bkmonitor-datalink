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
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fatih/color"
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
		//fmt.Println("bkmonitorbeat process may not running, please check!")
		color.Red("bkmonitorbeat process may not running, please check!")
	} else {
		color.Green("bkmonitorbeat process status is ok!")
		//fmt.Println("bkmonitorbeat process status is ok!")
	}

	ckSocket := checkDomainSocket()
	if !ckSocket {
		//fmt.Println("unable to connect unix domain socket, please check socket file.")
		color.Red("unable to connect unix domain socket, please check socket file.")
	} else {
		//fmt.Println("bkmonitorbeat unix domain socket status is ok!")
		color.Green("bkmonitorbeat unix domain socket status is ok!")
	}
	checkLog()
}

// CheckPidProcess 检测对应 Pid 的进程是否存在
func checkPidProcess() bool {
	var running bool
	pid := config.GetProcessPid()

	// 无法读取 pidFile 以及 pid 为空的情况
	if pid == "" {
		color.Red("pid is empty, unable to check bkmonitorbeat process\n")
		return running
	}

	// 尝试捕获特定 pid 的进程
	pid32, err := strconv.ParseInt(pid, 10, 32)
	if err != nil {
		color.Red("transform string pid to int32 error:%s \n", err)
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
		color.Red("unable to get the path of socket file\n")
		return socketFlag
	}

	// 当 err != nil  的时候说明 DomainSocket 文件夹不存在或其他错误
	_, err := os.Stat(socketPath)
	if err != nil {
		color.Red("unable to get socket file:%s error: %s\n", socketPath, err)
		return socketFlag
	}

	// 尝试通过 DomainSocket 文件建立连接
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		color.Red("unable to connect unix socket, error: %s\n", err)
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
		color.Red("unable to get bkmonitorbeat log path\n")
		return
	}

	files, err := os.ReadDir(logDir)
	// 无法读取文件夹的情况
	if err != nil {
		color.Red("unable to open logDir: %s, error: %s\n", logDir, err)
		return
	}

	logs := make([]os.DirEntry, 0)
	for _, v := range files {
		// 尝试获取所有 bkmonitorbeat 的日志文件
		if strings.Contains(v.Name(), "bkmonitorbeat") {
			logs = append(logs, v)
		}
	}
	// 日志文件不存在则不继续
	if len(logs) == 0 {
		color.Red("logDir: %s is empty\n", logDir)
		return
	}

	// 因为对于日志保留的情况，默认是存 7 天的数据，所以仅检测最近的日志文件，全量读取 去重处理
	// bkmonitorbeat、bkmonitorbeat.1、bkmonitorbeat.log
	// 检测 bkmonitorbeat.1 的情况是为了防止 bkmonitorbeat.log 刚刚进行切换 数量不足
	// 全量扫描日志文件 去重输出 额外增加关键词检测功能

	FileNames := []string{"bkmonitorbeat", "bkmonitorbeat.log", "bkmonitorbeat.1"}
	for _, filename := range FileNames {
		filePath := filepath.Join(logDir, filename)
		color.Yellow("start to scan the log: %s\n", filename)
		// 这里快速检测 不做关键词检索
		scanLogFile(filePath, nil)
	}

}

// scanFile 全量扫描文件，去重后输出
func scanLogFile(filePath string, keywords []string) {
	if filePath == "" {
		color.Red("unable to scan log file，path is empty!")
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		color.Red("unable to open filePath: %s, error: %s\n", filePath, err)
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := make([]string, 0)
	uniqueMap := make(map[string]bool)
	for scanner.Scan() {
		line := strings.Join(strings.Fields(scanner.Text()), " ")
		// 切分数据，仅获取 时间、日志级别、日志内容，然后根据日志内容进行去重
		content := strings.SplitN(line, " ", 3)
		if len(content) < 3 {
			continue
		}
		// 对日志内容进行 hash 获取唯一key
		hash := sha256.Sum256([]byte(content[2]))
		hashKey := hex.EncodeToString(hash[:])
		if uniqueMap[hashKey] {
			continue
		}
		// 关键词匹配
		if len(keywords) == 0 {
			uniqueMap[hashKey] = true
			lines = append(lines, scanner.Text())
		} else {
			matched := true
			for _, keyword := range keywords {
				if !strings.Contains(scanner.Text(), keyword) {
					matched = false
					break
				}
			}
			if matched {
				uniqueMap[hashKey] = true
				lines = append(lines, scanner.Text())
			}
		}
	}

	// 对于扫描的过程中产生了错误，则不对数据进行输出，直接返回
	if err = scanner.Err(); err != nil {
		color.Red("an error occurred while scanning the file, error: %s\n", err)
		return
	}

	c := color.New(color.Bold)
	for _, line := range lines {
		c.Println(line)
	}
}
