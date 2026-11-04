// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build activeprofile || basetask

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 立即执行由 BK-JOB 在目标主机上拉起同一个二进制的轻量模式，配置从 stdin 读入。
// 取消整次执行同样通过新的 JOB 下发一次取消命令。
var (
	activeProfileRun = flag.Bool(
		"active-profile-run",
		false,
		"Run one immediate active profiling execution, reading its single-host config as JSON from stdin",
	)
	activeProfileCancel = flag.Bool(
		"active-profile-cancel",
		false,
		"Cancel one immediate active profiling execution, reading its execution key as JSON from stdin",
	)
	// 匹配预览供页面在提交前确认采集目标：JOB 执行它并回收 stdout，SaaS 直接解析。
	activeProfileMatch = flag.Bool(
		"active-profile-match",
		false,
		"Match host processes by the process_selector read as JSON from stdin, printing the result as JSON",
	)
)

// handleActiveProfileCLI 在 Beat 框架初始化之前处理轻量模式，处理完直接退出。
func handleActiveProfileCLI() {
	modes := 0
	for _, enabled := range []bool{*activeProfileRun, *activeProfileCancel, *activeProfileMatch} {
		if enabled {
			modes++
		}
	}
	if modes == 0 {
		return
	}
	if modes > 1 {
		fmt.Println("active-profile-run, active-profile-cancel and active-profile-match are mutually exclusive")
		os.Exit(1)
	}

	var err error
	switch {
	case *activeProfileMatch:
		// 结果要被 SaaS 直接解析，stdout 只能有 JSON，因此这一模式丢弃日志而不是让
		// 单个进程的 WARN 混进结果里。失败由非 0 退出码表达，stdout 是失败原因的 JSON。
		logger.SetOptions(logger.Options{DevNull: true})
		if err := active_profile.MatchCLI(os.Stdin, os.Stdout); err != nil {
			active_profile.WriteMatchFailure(os.Stdout, err)
			os.Exit(1)
		}
		os.Exit(0)
	case *activeProfileRun:
		logger.SetOptions(logger.Options{Stdout: true, Level: "info"})
		err = active_profile.RunCLI(os.Stdin)
	default:
		logger.SetOptions(logger.Options{Stdout: true, Level: "info"})
		err = active_profile.CancelCLI(os.Stdin)
	}
	if err != nil {
		fmt.Printf("active profile cli failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
