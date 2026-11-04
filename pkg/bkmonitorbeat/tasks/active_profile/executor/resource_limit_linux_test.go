// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/cgroups"
)

func TestResourceLimitParentV2(t *testing.T) {
	cases := []struct {
		name    string
		content string
		parent  string
		ok      bool
	}{
		{name: "collector at root", content: "0::/collector-bkmonitorbeat-5a61\n", parent: "/", ok: true},
		{name: "nested collector", content: "0::/gse.slice/collector-bkmonitorbeat-5a61\n", parent: "/gse.slice", ok: true},
		{name: "not limited", content: "0::/system.slice/gse_agent.service\n", ok: false},
	}
	for _, c := range cases {
		parent, ok := resourceLimitParentV2(c.content)
		if parent != c.parent || ok != c.ok {
			t.Errorf("%s: resourceLimitParentV2() = %q, %v, want %q, %v", c.name, parent, ok, c.parent, c.ok)
		}
	}
}

// v1 只移动 Beat 处在 resource_limit cgroup 的子系统，systemd 等其它层级保持原样。
func TestResourceLimitParentPathV1(t *testing.T) {
	current := func(name cgroups.Name) (string, error) {
		switch name {
		case cgroups.Cpu, cgroups.Memory:
			return "/collector-bkmonitorbeat-5a61", nil
		case cgroups.SystemdDbus:
			return "/system.slice/gse_agent.service", nil
		default:
			return "", cgroups.ErrControllerNotActive
		}
	}
	path := resourceLimitParentPath(current)
	for _, name := range []cgroups.Name{cgroups.Cpu, cgroups.Memory} {
		if parent, err := path(name); err != nil || parent != "/" {
			t.Errorf("%s: parent = %q, %v, want /", name, parent, err)
		}
	}
	for _, name := range []cgroups.Name{cgroups.SystemdDbus, cgroups.Blkio} {
		if _, err := path(name); err != cgroups.ErrControllerNotActive {
			t.Errorf("%s: err = %v, want ErrControllerNotActive", name, err)
		}
	}
}

// 开关打开的任务拉起的外部进程 oom_score_adj 调到 1000；开关关闭时进程已移出 Beat 的
// cgroup，保持继承值。
func TestPreferChildOOMKill(t *testing.T) {
	inherited := readOOMScoreAdj(t, os.Getpid())
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "switch on", ctx: context.Background(), want: "1000"},
		{name: "switch off", ctx: withOutsideResourceLimit(context.Background()), want: inherited},
	}
	for _, c := range cases {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		preferChildOOMKill(c.ctx, cmd.Process.Pid)
		got := readOOMScoreAdj(t, cmd.Process.Pid)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if got != c.want {
			t.Errorf("%s: oom_score_adj = %s, want %s", c.name, got, c.want)
		}
	}
}

func readOOMScoreAdj(t *testing.T, pid int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "oom_score_adj"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
