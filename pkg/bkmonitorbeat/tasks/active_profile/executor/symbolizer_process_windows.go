// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build windows

package executor

import (
	"os"
	"os/exec"
	"path/filepath"
)

func resolveTrustedExternalCommand(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return resolved
}

func configureExternalCommand(_ *exec.Cmd) {}

func killExternalCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// Windows 没有 SIGINT 语义的进程组中断，直接结束进程。Active Profiling 只在 Linux
// 上采集，这里仅为可编译保留实现。
func interruptExternalCommand(cmd *exec.Cmd) error {
	return killExternalCommand(cmd)
}
