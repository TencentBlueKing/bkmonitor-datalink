// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// cancelMarkerFileName 是取消命令写入运行目录的标记文件。采集进程轮询它来取消
	// 自己的执行 context。用文件而不是信号，是因为计划任务的采集只是常驻 Beat 里的
	// 一个 goroutine：给进程发信号会影响整个 Beat 和同进程的其他任务。
	cancelMarkerFileName = "cancel.request"

	// cancelMarkerPollInterval 决定取消的最大延迟。相对 15~600 秒的采样窗口可忽略。
	cancelMarkerPollInterval = time.Second
)

// buildExecutionKey 是执行键的唯一构造入口。采集端和取消端都必须经由它，否则两边
// 一旦分叉就会算出不同的运行目录，取消永远定位不到采集进程。
func buildExecutionKey(tenantID string, bizID, taskID int32, scheduledAtMS int64, bkHostID int64) string {
	return fmt.Sprintf("%s|%d|%d|%d|%d", tenantID, bizID, taskID, scheduledAtMS, bkHostID)
}

// executionKey 是一台主机上一次执行的定位键，三类执行都按 task_id + scheduled_at
// 定位；不引入 run_id。
func executionKey(conf *configs.ActiveProfileTaskConfig, scheduledAt time.Time) string {
	return buildExecutionKey(conf.BKTenantID, conf.GetBizID(), conf.GetTaskID(), scheduledAt.UnixMilli(), conf.BKHostID)
}

// runDirFor 返回按执行键确定的运行目录。目录名只是本地路径，不进入业务协议；
// 采样文件、转换临时文件和清理范围都限定在其中，不同执行并行时互不覆盖。
func runDirFor(outputDir, key string) string {
	digest := sha1.Sum([]byte(key))
	return filepath.Join(outputDir, "runs", fmt.Sprintf("%x", digest[:10]))
}

// runContext 拥有一次执行的运行目录生命周期。
type runContext struct {
	Key string
	Dir string
}

// openRunContext 创建本次执行的运行目录。
func openRunContext(conf *configs.ActiveProfileTaskConfig, scheduledAt time.Time) (*runContext, error) {
	key := executionKey(conf, scheduledAt)
	dir := runDirFor(conf.OutputDir, key)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create run directory %s: %w", dir, err)
	}
	return &runContext{Key: key, Dir: dir}, nil
}

// Close 删除整个运行目录。保留产物用于受控调试时不删除。
func (r *runContext) Close(keepArtifacts bool) {
	if r == nil || keepArtifacts {
		return
	}
	if err := os.RemoveAll(r.Dir); err != nil {
		logger.Errorf("[active_profile] cleanup run directory %s failed: %v", r.Dir, err)
	}
}

// requestCancel 为指定执行写入取消标记。目录可能还不存在（取消命令早于采集启动），
// 因此一并创建：采集启动时会先检查一次标记并立即结束。
func requestCancel(outputDir, key string) (string, error) {
	dir := runDirFor(outputDir, key)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create run directory %s: %w", dir, err)
	}
	marker := filepath.Join(dir, cancelMarkerFileName)
	if err := os.WriteFile(marker, []byte(key+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write cancel marker %s: %w", marker, err)
	}
	return marker, nil
}

// cancelRequested 判断本次执行是否已被请求取消。
func cancelRequested(runDir string) bool {
	_, err := os.Stat(filepath.Join(runDir, cancelMarkerFileName))
	return err == nil
}

// watchCancelMarker 轮询取消标记并在发现后取消本次执行的 context。watcher 的作用域
// 就是这一轮执行：它只做 os.Stat，随执行结束退出，不与同进程的其他任务共享状态，
// 因此常驻 Beat 里的其他采集任务不受影响。
func watchCancelMarker(ctx context.Context, runDir string, cancel context.CancelCauseFunc) func() {
	if cancelRequested(runDir) {
		cancel(ErrExecutionCancelled)
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(cancelMarkerPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if cancelRequested(runDir) {
					logger.Warnf("[active_profile] cancel requested for run directory %s", runDir)
					cancel(ErrExecutionCancelled)
					return
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// sweepStaleRunDirs 清理上一次进程异常退出后残留的运行目录，以及给从未执行的
// occurrence 写下的取消标记。perf.data 在 system 范围或 DWARF 模式下可能很大，
// 崩溃残留必须在下次启动时回收。
func sweepStaleRunDirs(outputDir string, maxAge time.Duration) {
	root := filepath.Join(outputDir, "runs")
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warnf("[active_profile] scan stale run directories in %s failed: %v", root, err)
		}
		return
	}
	deadline := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(deadline) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			logger.Warnf("[active_profile] remove stale run directory %s failed: %v", path, err)
			continue
		}
		logger.Infof("[active_profile] removed stale run directory %s", path)
	}
}
