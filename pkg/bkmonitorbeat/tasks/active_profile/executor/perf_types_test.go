// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

func validPerfConfig() perfConfig {
	return perfConfig{
		DurationSec:         15,
		FrequencyHz:         99,
		PIDs:                []int32{2341, 2342},
		CallGraph:           callGraphFP,
		InlineSymbolization: inlineSymbolizationAuto,
		Event:               eventCPUClock,
		PerfPath:            "perf",
		OutputDir:           "/tmp/profile",
		Labels: SampleLabels{
			BizID:       11,
			TaskID:      1001,
			ScheduledAt: 1786521405518,
			BKHostID:    456,
			IP:          "192.0.2.10",
		},
	}
}

// unknown 帧的 DSO 归类是运维定位"函数名缺失"的唯一依据，格式要稳定：按转换器给出的
// 顺序原样拼接，没有 unknown 帧时返回空串，让调用方跳过这行日志。
func TestUnknownDSOSummary(t *testing.T) {
	if got := (&Result{}).UnknownDSOSummary(); got != "" {
		t.Fatalf("UnknownDSOSummary() = %q, want empty when nothing is unknown", got)
	}
	result := &Result{UnknownDSOTop: []dsoCount{
		{DSO: "/usr/local/bin/workload", Count: 812},
		{DSO: "[unknown]", Count: 34},
	}}
	if want := "[/usr/local/bin/workload=812 [unknown]=34]"; result.UnknownDSOSummary() != want {
		t.Fatalf("UnknownDSOSummary() = %q, want %q", result.UnknownDSOSummary(), want)
	}
}

func TestPerfConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*perfConfig)
		wantErr bool
	}{
		{name: "multiple pids"},
		{
			name: "single pid",
			mutate: func(c *perfConfig) {
				c.PIDs = []int32{2341}
			},
		},
		{
			name: "system wide is only allowed without target pids",
			mutate: func(c *perfConfig) {
				c.SystemWide = true
				c.PIDs = nil
			},
		},
		{
			name: "system wide rejects target pids",
			mutate: func(c *perfConfig) {
				c.SystemWide = true
			},
			wantErr: true,
		},
		{
			name: "no target",
			mutate: func(c *perfConfig) {
				c.PIDs = nil
			},
			wantErr: true,
		},
		{
			name: "non positive pid",
			mutate: func(c *perfConfig) {
				c.PIDs = []int32{2341, 0}
			},
			wantErr: true,
		},
		{
			name: "dwarf call graph",
			mutate: func(c *perfConfig) {
				c.CallGraph = callGraphDWARF
			},
		},
		{
			name: "unsupported call graph",
			mutate: func(c *perfConfig) {
				c.CallGraph = "lbr"
			},
			wantErr: true,
		},
		{
			name: "unsupported inline symbolization",
			mutate: func(c *perfConfig) {
				c.InlineSymbolization = "required"
			},
			wantErr: true,
		},
		{
			name: "unsupported event",
			mutate: func(c *perfConfig) {
				c.Event = "cycles"
			},
			wantErr: true,
		},
		{
			name: "duration too large",
			mutate: func(c *perfConfig) {
				c.DurationSec = configs.ActiveProfileMaxDurationSec + 1
			},
			wantErr: true,
		},
		{
			name: "frequency too high",
			mutate: func(c *perfConfig) {
				c.FrequencyHz = configs.ActiveProfileMaxFrequencyHz + 1
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := validPerfConfig()
			if tt.mutate != nil {
				tt.mutate(&conf)
			}
			err := conf.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
