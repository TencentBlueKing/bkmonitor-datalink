// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

//go:build !linux

package redistest

import "os/exec"

// lifeline does nothing where the kernel cannot tie a child to its parent;
// the registry's reaping is what ends an orphan there (orphans.go).
func lifeline(*exec.Cmd) {}
