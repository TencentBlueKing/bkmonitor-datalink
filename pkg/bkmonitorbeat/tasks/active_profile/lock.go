// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// errTaskAlreadyRunning 是抢锁失败时写入 finished Event 的忙碌原因。抢锁失败不
// 等待、不排队，也不启动采集器。
const errTaskAlreadyRunning = "another run of the same scheduled task is still in progress on this host"

// taskRunLock 是同一主机上同一个计划 task 的跨进程互斥锁。锁域为
// bk_tenant_id + bk_biz_id + task_id，只对 once_at/cron 生效，立即执行不参与。
// 使用文件锁而不是进程内布尔值，才能覆盖 reload、重启和多 Beat 进程重叠。
type taskRunLock struct {
	path string
	file *os.File
}

// acquireTaskRunLock 以非阻塞方式抢锁。第二个返回值为 false 表示同一 task 的另一
// 轮执行仍在进行中。
func acquireTaskRunLock(conf *configs.ActiveProfileTaskConfig) (*taskRunLock, bool, error) {
	if !conf.IsPlanned() {
		return nil, true, nil
	}
	key := fmt.Sprintf("%s|%d|%d", conf.BKTenantID, conf.GetBizID(), conf.GetTaskID())
	digest := sha1.Sum([]byte(key))
	dir := filepath.Join(conf.OutputDir, "locks")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, false, fmt.Errorf("create lock directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("task_%x.lock", digest[:10]))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open lock file %s: %w", path, err)
	}
	acquired, err := tryLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	if !acquired {
		_ = file.Close()
		return nil, false, nil
	}
	return &taskRunLock{path: path, file: file}, true, nil
}

// Release 在正常路径显式释放并关闭锁文件。持锁进程被取消或异常退出时，操作系统
// 也会自动释放该锁。
func (l *taskRunLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	if err := unlockFile(l.file); err != nil {
		logger.Warnf("[active_profile] unlock %s failed: %v", l.path, err)
	}
	if err := l.file.Close(); err != nil {
		logger.Warnf("[active_profile] close lock file %s failed: %v", l.path, err)
	}
	l.file = nil
}
