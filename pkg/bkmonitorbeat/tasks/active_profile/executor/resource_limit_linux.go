// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/cgroups"
)

// cgroupV2Mountpoint 与 libgse 创建 resource_limit cgroup 时使用的挂载点一致。
const cgroupV2Mountpoint = "/sys/fs/cgroup"

// moveOutOfResourceLimit 按 libgse 选择 cgroup 版本的同一规则分流。Beat 不在
// resource_limit cgroup 里时无事可做。
func moveOutOfResourceLimit(pid int) error {
	switch cgroups.Mode() {
	case cgroups.Unified:
		return moveOutOfResourceLimitV2(pid)
	case cgroups.Legacy, cgroups.Hybrid:
		return moveOutOfResourceLimitV1(pid)
	default:
		return nil
	}
}

func moveOutOfResourceLimitV2(pid int) error {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	parent, ok := resourceLimitParentV2(string(raw))
	if !ok {
		return nil
	}
	procs := filepath.Join(cgroupV2Mountpoint, parent, "cgroup.procs")
	return os.WriteFile(procs, []byte(strconv.Itoa(pid)), 0o644)
}

// resourceLimitParentV2 从 /proc/self/cgroup 取 Beat 所在 resource_limit cgroup 的上一级。
func resourceLimitParentV2(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if ok && strings.HasPrefix(filepath.Base(path), resourceLimitCgroupPrefix) {
			return filepath.Dir(path), true
		}
	}
	return "", false
}

func moveOutOfResourceLimitV1(pid int) error {
	cgroup, err := cgroups.Load(cgroups.V1, resourceLimitParentPath(cgroups.PidPath(os.Getpid())))
	if errors.Is(err, cgroups.ErrCgroupDeleted) {
		return nil
	}
	if err != nil {
		return err
	}
	return cgroup.Add(cgroups.Process{Pid: pid})
}

func raiseOOMScore(pid int) error {
	path := filepath.Join("/proc", strconv.Itoa(pid), "oom_score_adj")
	return os.WriteFile(path, []byte("1000"), 0o644)
}

// resourceLimitParentPath 只给 Beat 处在 resource_limit cgroup 的子系统返回上一级路径；
// 其余子系统（例如 systemd 的 name 层级）返回 ErrControllerNotActive，Load 会跳过。
func resourceLimitParentPath(current cgroups.Path) cgroups.Path {
	return func(name cgroups.Name) (string, error) {
		path, err := current(name)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(filepath.Base(path), resourceLimitCgroupPrefix) {
			return "", cgroups.ErrControllerNotActive
		}
		return filepath.Dir(path), nil
	}
}
