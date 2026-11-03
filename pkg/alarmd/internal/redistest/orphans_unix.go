// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

//go:build unix

package redistest

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// processGone is whether no process has the pid. A process of another user
// answers EPERM and is not gone.
func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// killServerProcess kills pid when it is a redis-server whose title names
// address - the title redis-server sets for itself, "redis-server host:port" -
// and says whether it did.
func killServerProcess(pid int, address string) bool {
	// args, not comm: macOS cuts comm to 16 bytes, "/usr/local/bin/r".
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || filepath.Base(fields[0]) != "redis-server" || fields[1] != address {
		return false
	}
	return syscall.Kill(pid, syscall.SIGKILL) == nil
}
