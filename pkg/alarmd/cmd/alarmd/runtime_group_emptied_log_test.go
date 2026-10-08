// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// groupEmptiedLines is every target_group emptying line the log holds.
func groupEmptiedLines(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if event["stage"] == "target_group" {
			lines = append(lines, event)
		}
	}
	return lines
}

// The group store the production wiring builds says, on its log, when it
// holds back an emptying it judged the writer's - every referenced group
// read empty at once - and when the writer's members come back: a warning
// with the groups held and the groups that had members, then one line that
// the hold ended.
func TestTheProductionGroupStoreLogsAnEmptyingItHoldsAndItsEnd(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	prefix := "test_prefix:"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	members := map[string]string{
		"1": `{"model_id":"cw-Host","model_inst_ids":["1"],"member_list":[{"model_id":"cw-Host","model_inst_id":"1","bk_host_id":1}]}`,
		"2": `{"model_id":"cw-Host","model_inst_ids":["2"],"member_list":[{"model_id":"cw-Host","model_inst_id":"2","bk_host_id":2}]}`,
	}
	write := func(values map[string]string) {
		t.Helper()
		for id, value := range values {
			if err := client.Set(ctx, prefix+"dynamic_group:"+id, value, 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(members)

	var output bytes.Buffer
	logger := observability.New(observability.ComponentRuntime, &output)
	_, groups, err := buildTargetResolver(cfg, client, nil, 1<<20, logger)
	if err != nil || groups == nil {
		t.Fatalf("group store = %v, %v", groups, err)
	}
	for id := range members {
		if lookup := groups.Group(ctx, id, time.Minute); lookup.Snapshot == nil || len(lookup.Snapshot.Members) != 1 {
			t.Fatalf("setup: group %s = %+v", id, lookup)
		}
	}

	empty := `{"model_id":"cw-Host","model_inst_ids":[],"member_list":[]}`
	write(map[string]string{"1": empty, "2": empty})
	if err := groups.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	lines := groupEmptiedLines(t, &output)
	if len(lines) != 1 || lines[0]["level"] != "WARN" || lines[0]["result"] != "emptied_held" || lines[0]["records"] != float64(2) ||
		lines[0]["held"] != float64(2) || lines[0]["had_members"] != float64(2) {
		t.Fatalf("log after every group emptied = %v, want one warning: 2 held of 2 that had members", lines)
	}

	write(members)
	if err := groups.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	lines = groupEmptiedLines(t, &output)
	if len(lines) != 2 || lines[1]["level"] != "INFO" || lines[1]["result"] != "emptied_released" || lines[1]["held"] != float64(0) {
		t.Fatalf("log after the members came back = %v, want a second line that the hold ended", lines)
	}
}
