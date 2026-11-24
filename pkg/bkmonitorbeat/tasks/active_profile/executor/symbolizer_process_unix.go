// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !windows

package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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
	if !trustedExternalPath(resolved, false) {
		return ""
	}
	for directory := filepath.Dir(resolved); ; directory = filepath.Dir(directory) {
		if !trustedExternalPath(directory, true) {
			return ""
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	return resolved
}

func trustedExternalPath(path string, directory bool) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if directory {
		if !info.IsDir() {
			return false
		}
	} else if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return false
	}
	// Beat may run as root. Never discover a tool through a directory or file
	// writable by another account, and only trust root or the current UID.
	if info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	effectiveUID := uint32(os.Geteuid())
	return stat.Uid == 0 || stat.Uid == effectiveUID
}

func configureExternalCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killExternalCommand(cmd *exec.Cmd) error {
	return signalExternalCommand(cmd, syscall.SIGKILL)
}

func interruptExternalCommand(cmd *exec.Cmd) error {
	return signalExternalCommand(cmd, syscall.SIGINT)
}

func signalExternalCommand(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
