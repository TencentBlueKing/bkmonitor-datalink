// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"os/exec"
	"syscall"
)

// lifeline has the kernel kill the server when the thread that started it
// exits, which a test binary ended without its cleanups does.
func lifeline(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
