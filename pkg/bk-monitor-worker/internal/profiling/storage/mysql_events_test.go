// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/models"
)

func TestPrepareExecutionEventChangesMergesNewAndCurrentHostConfig(t *testing.T) {
	existingConfig := json.RawMessage(`{
		"target_config":{"nodes":[{"bk_host_id":1}]},
		"process_selector":{"type":"pid","host_pids":[{"bk_host_id":1,"pids":[11]}]}
	}`)
	incomingConfig := json.RawMessage(`{
		"target_config":{"nodes":[
			{"bk_host_id":1,"ip":"existing"},
			{"bk_host_id":2,"ip":"new"},
			{"bk_host_id":3,"ip":"current"}
		]},
		"process_selector":{"type":"pid","host_pids":[
			{"bk_host_id":1,"pids":[111]},
			{"bk_host_id":2,"pids":[22]},
			{"bk_host_id":3,"pids":[33]}
		]}
	}`)
	event := core.Event{
		Name:      core.EventFinished,
		Timestamp: 20,
		HostID:    3,
		Status:    "succeed",
		Content: core.EventContent{
			StartedAt:  10,
			FinishedAt: 20,
			Config:     incomingConfig,
			TargetHosts: []core.TargetHost{
				{HostID: 1},
				{HostID: 2},
				{HostID: 3},
			},
		},
	}
	hosts := map[int]models.ActiveProfilingHostExecution{
		1: {BkHostID: 1, Status: core.StatusRunning, EventTime: 5},
		3: {BkHostID: 3, Status: core.StatusRunning, EventTime: 5},
	}

	changes, err := prepareExecutionEventChanges(
		executionRow{ID: 7, ScheduledAt: 1, Config: existingConfig},
		[]core.Event{event},
		hosts,
		time.UnixMilli(20),
		20,
		core.WindowPolicy{},
	)
	if err != nil {
		t.Fatalf("prepare event changes: %s", err)
	}
	if _, exists := hosts[2]; exists || hosts[3].Status != core.StatusRunning {
		t.Fatalf("preparing changes mutated the input host snapshot: %v", hosts)
	}
	var got struct {
		TargetConfig struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"target_config"`
		ProcessSelector struct {
			HostPIDs []map[string]any `json:"host_pids"`
		} `json:"process_selector"`
	}
	if err := json.Unmarshal([]byte(changes.config), &got); err != nil {
		t.Fatalf("decode merged config: %s", err)
	}

	nodes := make(map[int]map[string]any, len(got.TargetConfig.Nodes))
	for _, node := range got.TargetConfig.Nodes {
		id, ok := objectHostID(node)
		if !ok {
			t.Fatalf("node has invalid host id: %v", node)
		}
		nodes[id] = node
	}
	if _, exists := nodes[1]["ip"]; exists {
		t.Fatalf("existing non-event host config was unexpectedly replaced: %v", nodes[1])
	}
	for id, wantIP := range map[int]string{2: "new", 3: "current"} {
		if gotIP := nodes[id]["ip"]; gotIP != wantIP {
			t.Fatalf("host %d config ip = %v, want %q", id, gotIP, wantIP)
		}
	}

	pids := make(map[int]map[string]any, len(got.ProcessSelector.HostPIDs))
	for _, item := range got.ProcessSelector.HostPIDs {
		id, ok := objectHostID(item)
		if !ok {
			t.Fatalf("host PID entry has invalid host id: %v", item)
		}
		pids[id] = item
	}
	if gotPIDs := pids[1]["pids"]; gotPIDs == nil {
		t.Fatalf("existing host PID config was lost: %v", pids[1])
	}
	for _, id := range []int{2, 3} {
		if pids[id] == nil {
			t.Fatalf("new or current event host %d PID config is missing", id)
		}
	}
}
