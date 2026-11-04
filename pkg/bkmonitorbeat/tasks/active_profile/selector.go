// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	shiroupsnet "github.com/shirou/gopsutil/v3/net"
	shiroups "github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile/executor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// errNoProcessMatched 表示进程解析阶段没有得到任何可采集的进程。一期约定匹配结果
// 为空即在解析阶段失败，不会继续启动采集器。
var errNoProcessMatched = errors.New("process selector matched no process")

// socketStatusListen 是 gopsutil 表示监听态 TCP socket 的状态值。
const socketStatusListen = "LISTEN"

// maxPIDFileBytes 只给 PID 文件读取设上限，防止把 /dev/zero 这类读不到结尾的文件或
// 超大文件读进内存。PID 最多 7 位（pid_max 上限 2^22），合法的 PID 文件碰不到一页。
const maxPIDFileBytes = 4096

// resolveProcesses 按下发的 process_selector 在本机解析本次采集的全部同类型进程。
// 开启动态采集的命令行选择器每次执行都重新匹配，不设进程数量上限；关闭动态采集时与
// PID 模式一样只采集勾选的 PID，不再按命令行校验。PID 与 PID 文件模式只保留当前仍然
// 存在的进程，长期任务中 PID 失效导致采集不到属于预期结果。
//
// 采集与匹配预览命令共用它，两者对同一份选择器的匹配结果因此不可能分叉。
func resolveProcesses(selector *configs.ActiveProfileProcessSelector) ([]ProcessResult, error) {
	switch selector.Type {
	case configs.ActiveProfileSelectorCommand:
		if !selector.EnableDynamicCollection {
			return resolveByPIDs(selector.TargetPIDs())
		}
		return resolveByCommand(selector)
	case configs.ActiveProfileSelectorPIDFile:
		return resolveByPIDFile(selector)
	case configs.ActiveProfileSelectorPID:
		return resolveByPIDs(selector.TargetPIDs())
	case configs.ActiveProfileSelectorSystem:
		// 本地调试用的 system 范围采集不解析进程，样本仍由 pprof 的 pid/comm 区分。
		return nil, nil
	default:
		return nil, executor.MarkError(
			executor.ErrInvalidConfig,
			fmt.Errorf("unsupported process_selector.type %q", selector.Type),
		)
	}
}

// resolveGoEndpointProcess 按 pprof endpoint 的监听端口反查进程，用于补齐 Go 产物的
// pid/comm 标签与 finished Event 的 processes。Go 的 pprof 响应不携带进程信息，这是
// 唯一来源，而反查依赖读取本机全部 socket 的归属进程，权限不足、endpoint 由其他网络
// 命名空间提供、或端口被多个进程以 SO_REUSEPORT 共享时都拿不到唯一结果。协议约定
// 这里是尽力而为，因此任何一种拿不到的情况都只告警，不影响采集本身。
func resolveGoEndpointProcess(ctx context.Context, port int) []ProcessResult {
	connections, err := shiroupsnet.ConnectionsWithoutUidsWithContext(ctx, "tcp")
	if err != nil {
		logger.Warnf("[active_profile] list host tcp sockets for pprof endpoint port %d failed: %v", port, err)
		return nil
	}
	matched := make([]int32, 0, 1)
	seen := make(map[int32]struct{}, 1)
	for _, connection := range connections {
		if connection.Status != socketStatusListen || int(connection.Laddr.Port) != port || connection.Pid <= 0 {
			continue
		}
		if _, exists := seen[connection.Pid]; exists {
			continue
		}
		seen[connection.Pid] = struct{}{}
		matched = append(matched, connection.Pid)
	}
	if len(matched) != 1 {
		logger.Warnf(
			"[active_profile] pprof endpoint port %d resolved to %d listening processes; go profile will carry no pid/comm label",
			port, len(matched),
		)
		return nil
	}

	pid := matched[0]
	process, err := shiroups.NewProcess(pid)
	if err != nil {
		logger.Warnf("[active_profile] pprof endpoint port %d listener pid %d is unavailable: %v", port, pid, err)
		return nil
	}
	name, err := process.Name()
	if err != nil {
		logger.Warnf("[active_profile] read process name of pid %d failed: %v", pid, err)
	}
	return []ProcessResult{{PID: pid, ProcessName: name}}
}

// resolveByCommand 与现有进程采集保持同样的两段判断：先按可选排除正则跳过，再判断
// 包含条件。按协议约定，match_pattern 是命令行的直接包含字符串，不作为正则解释。
func resolveByCommand(selector *configs.ActiveProfileProcessSelector) ([]ProcessResult, error) {
	pids, err := shiroups.Pids()
	if err != nil {
		return nil, fmt.Errorf("list host processes: %w", err)
	}
	exclude := selector.ExcludeRegexp()
	matched := make([]ProcessResult, 0, 4)
	for _, pid := range pids {
		// 不用 NewProcess：它会顺带计算启动时间，每个进程都要读 /proc/<pid>/stat 并重读
		// 整份 /proc/stat，全机扫描时这部分开销随进程数线性放大，而匹配只需要命令行。
		// 进程已退出时读 cmdline 失败，同样跳过。
		process := &shiroups.Process{Pid: pid}
		cmdline, err := process.Cmdline()
		if err != nil || cmdline == "" {
			continue
		}
		if exclude != nil && exclude.MatchString(cmdline) {
			continue
		}
		if !strings.Contains(cmdline, selector.MatchPattern) {
			continue
		}
		name, err := process.Name()
		if err != nil {
			logger.Warnf("[active_profile] read process name of pid %d failed: %v", pid, err)
		}
		matched = append(matched, ProcessResult{PID: pid, ProcessName: name})
	}
	if len(matched) == 0 {
		return nil, errNoProcessMatched
	}
	return sortProcesses(matched), nil
}

// resolveByPIDFile 沿用现有进程采集读取 PID 文件的方式：整文件去空白后解析单个 PID。
// resolveByPIDFile 逐个读取 PID 文件。同一组路径下发到所有目标主机，不同主机上的服务
// 未必相同，读不到或内容非法的文件只告警跳过；一个 PID 都读不到按未匹配到进程处理，
// 并带上各文件的原因写入 finished 的 message。
func resolveByPIDFile(selector *configs.ActiveProfileProcessSelector) ([]ProcessResult, error) {
	pids := make([]int32, 0, len(selector.PIDPaths()))
	var errs []error
	for _, pidPath := range selector.PIDPaths() {
		pid, err := readPIDFile(pidPath)
		if err != nil {
			logger.Warnf("[active_profile] skip pid file: %v", err)
			errs = append(errs, err)
			continue
		}
		pids = append(pids, pid)
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("%w: %w", errNoProcessMatched, errors.Join(errs...))
	}
	return resolveByPIDs(pids)
}

// readPIDFile 只读普通文件，且错误信息不回显文件内容：pid_path 由平台用户填写而
// Beat 以 root 读取，回显内容就等于让用户读出主机上的任意文件。非阻塞打开是为了
// 路径指向 FIFO 时不一直等写端，随后由普通文件校验拒绝。
func readPIDFile(pidPath string) (int32, error) {
	file, err := os.OpenFile(pidPath, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		return 0, fmt.Errorf("read pid file %s: %w", pidPath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("read pid file %s: %w", pidPath, err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("pid file %s is not a regular file", pidPath)
	}
	// 多读一个字节用于区分"正好等于上限"和"超过上限"。
	content, err := io.ReadAll(io.LimitReader(file, maxPIDFileBytes+1))
	if err != nil {
		return 0, fmt.Errorf("read pid file %s: %w", pidPath, err)
	}
	if len(content) > maxPIDFileBytes {
		return 0, fmt.Errorf("pid file %s exceeds %d bytes", pidPath, maxPIDFileBytes)
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("pid file %s does not contain a valid pid", pidPath)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("pid file %s contains invalid pid %d", pidPath, pid)
	}
	return int32(pid), nil
}

func resolveByPIDs(pids []int32) ([]ProcessResult, error) {
	alive := make([]ProcessResult, 0, len(pids))
	seen := make(map[int32]struct{}, len(pids))
	for _, pid := range pids {
		if _, exists := seen[pid]; exists {
			continue
		}
		seen[pid] = struct{}{}
		process, err := shiroups.NewProcess(pid)
		if err != nil {
			logger.Warnf("[active_profile] target pid %d is unavailable: %v", pid, err)
			continue
		}
		name, err := process.Name()
		if err != nil {
			logger.Warnf("[active_profile] read process name of pid %d failed: %v", pid, err)
		}
		alive = append(alive, ProcessResult{PID: pid, ProcessName: name})
	}
	if len(alive) == 0 {
		return nil, errNoProcessMatched
	}
	return sortProcesses(alive), nil
}

// MatchedProcess 是匹配预览的返回项。它比 ProcessResult 多出命令行与启动时间，用于
// 让用户在页面上认出进程；这两个字段只服务预览，不进 Event——Event 的 processes 按
// 协议严格只有 pid 与 process_name。
type MatchedProcess struct {
	PID         int32  `json:"pid"`
	ProcessName string `json:"process_name"`
	Command     string `json:"command"`
	// StartedAt 是进程启动时间，Unix 毫秒。PID 会被系统回收，页面冻结选择结果时靠
	// 它与 pid 一起确认是不是同一个进程。
	StartedAt int64 `json:"started_at"`
}

// describeProcesses 为已匹配的进程补上命令行与启动时间。只对匹配结果再读一次
// /proc，命中集通常只有个位数，不值得为此把这两个字段塞进 ProcessResult。
//
// 读不到命令行的进程会被丢掉：预览的语义是"此刻匹配到的进程"，匹配之后立刻退出的
// 进程不该出现在给用户勾选的列表里。
func describeProcesses(processes []ProcessResult) []MatchedProcess {
	described := make([]MatchedProcess, 0, len(processes))
	for _, result := range processes {
		process, err := shiroups.NewProcess(result.PID)
		if err != nil {
			continue
		}
		cmdline, err := process.Cmdline()
		if err != nil || cmdline == "" {
			continue
		}
		startedAt, err := process.CreateTime()
		if err != nil {
			continue
		}
		described = append(described, MatchedProcess{
			PID:         result.PID,
			ProcessName: result.ProcessName,
			Command:     cmdline,
			StartedAt:   startedAt,
		})
	}
	return described
}

// sortProcesses 让 perf 参数、Event processes 和运行目录内容在同一次执行中保持稳定顺序。
func sortProcesses(processes []ProcessResult) []ProcessResult {
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].PID < processes[j].PID
	})
	return processes
}

func processPIDs(processes []ProcessResult) []int32 {
	pids := make([]int32, 0, len(processes))
	for _, process := range processes {
		pids = append(pids, process.PID)
	}
	return pids
}
