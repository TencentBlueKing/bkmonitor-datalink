// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 只统计前后两次都读到的进程：窗口内退出的进程读不到结束值，不能把它的起始值
// 当成消耗；时钟回退按 0 处理。
func TestTargetCPUUsedCountsOnlyProcessesReadTwice(t *testing.T) {
	before := map[int32]time.Duration{
		1: 100 * time.Millisecond,
		2: 50 * time.Millisecond,
		3: 30 * time.Millisecond,
	}
	after := map[int32]time.Duration{
		1: 112 * time.Millisecond,
		3: 20 * time.Millisecond,
		4: 900 * time.Millisecond,
	}

	used, measured := targetCPUUsed(before, after)
	if !measured || used != 12*time.Millisecond {
		t.Fatalf("targetCPUUsed() = %s, %t; want 12ms, true", used, measured)
	}
}

func TestTargetCPUUsedReportsUnmeasured(t *testing.T) {
	cases := map[string][2]map[int32]time.Duration{
		"system wide":        {nil, nil},
		"all targets exited": {{1: time.Second}, {}},
	}
	for name, maps := range cases {
		if used, measured := targetCPUUsed(maps[0], maps[1]); measured || used != 0 {
			t.Errorf("%s: targetCPUUsed() = %s, %t; want 0, false", name, used, measured)
		}
	}
}

// 提示要同时给出实际 CPU 占用和单个样本的门槛，用户才能看出是进程太闲而不是采集
// 故障；错误链必须保留 ErrEmptyProfile，上层据此归类为 empty_profile。
func TestEmptyProfileErrorExplainsIdleTarget(t *testing.T) {
	conf := validPerfConfig()
	conf.DurationSec = 30
	base := fmt.Errorf("perf script contains no samples: %w", ErrEmptyProfile)

	err := emptyProfileError(
		base, conf,
		map[int32]time.Duration{7: time.Second},
		map[int32]time.Duration{7: time.Second + 23800*time.Microsecond},
	)
	if !errors.Is(err, ErrEmptyProfile) {
		t.Fatalf("error chain lost ErrEmptyProfile: %v", err)
	}
	want := "perf script contains no samples: empty profile: " +
		"target processes used 23.8ms of CPU during the 30s sampling window; " +
		"at 99hz a sample needs 10.1ms of CPU from one thread on one CPU; " +
		"if the target is mostly idle, raise frequency_hz or duration_sec, or profile while it is busy"
	if err.Error() != want {
		t.Fatalf("message mismatch:\n got: %s\nwant: %s", err, want)
	}
}

// 读不到 CPU 时间时不能编造占用数字，只保留门槛和调参方向。
func TestEmptyProfileErrorWithoutMeasurementOmitsUsage(t *testing.T) {
	conf := validPerfConfig()
	conf.FrequencyHz = 999
	base := fmt.Errorf("perf script contains no samples: %w", ErrEmptyProfile)

	err := emptyProfileError(base, conf, nil, nil)
	if !errors.Is(err, ErrEmptyProfile) {
		t.Fatalf("error chain lost ErrEmptyProfile: %v", err)
	}
	message := err.Error()
	if strings.Contains(message, "target processes used") {
		t.Fatalf("unmeasured hint must not report usage: %s", message)
	}
	if !strings.Contains(message, "at 999hz a sample needs 1ms of CPU") {
		t.Fatalf("hint should state the sampling period: %s", message)
	}
}

// 目标在采样窗口内全部退出时，没有样本与频率无关：只说明进程退出，不给调参建议。
func TestEmptyProfileErrorReportsExitedTargets(t *testing.T) {
	conf := validPerfConfig()
	base := fmt.Errorf("perf script contains no samples: %w", ErrEmptyProfile)

	err := emptyProfileError(base, conf, map[int32]time.Duration{7: time.Second}, map[int32]time.Duration{})
	if !errors.Is(err, ErrEmptyProfile) {
		t.Fatalf("error chain lost ErrEmptyProfile: %v", err)
	}
	want := "perf script contains no samples: empty profile: " +
		"target processes exited during the 15s sampling window"
	if err.Error() != want {
		t.Fatalf("message mismatch:\n got: %s\nwant: %s", err, want)
	}
}
