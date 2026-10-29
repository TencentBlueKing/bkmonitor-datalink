// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
)

// Config merge helpers preserve host-scoped settings across Event updates.
func mergeConfig(
	existing json.RawMessage,
	incoming json.RawMessage,
	addedHosts map[int]struct{},
	snapshots []core.TargetHost,
	eventHostID int,
) (string, error) {
	base, err := decodeJSONObject(existing)
	if err != nil {
		return "", fmt.Errorf("decode stored config: %w", err)
	}
	if len(base) == 0 {
		base, err = decodeJSONObject(incoming)
		if err != nil {
			return "", fmt.Errorf("decode incoming config: %w", err)
		}
	}
	source, err := decodeJSONObject(incoming)
	if err != nil {
		return "", fmt.Errorf("decode incoming config: %w", err)
	}
	mergeConfigNodes(base, source, addedHosts, snapshots, eventHostID)
	mergeHostPIDs(base, source, addedHosts, eventHostID)
	encoded, err := json.Marshal(base)
	if err != nil {
		return "", fmt.Errorf("encode merged config: %w", err)
	}
	return string(encoded), nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}
	return value, nil
}

func mergeConfigNodes(
	base map[string]any,
	source map[string]any,
	addedHosts map[int]struct{},
	snapshots []core.TargetHost,
	eventHostID int,
) {
	sourceTarget, _ := source["target_config"].(map[string]any)
	var sourceNodes []any
	if sourceTarget != nil {
		sourceNodes, _ = sourceTarget["nodes"].([]any)
	}
	targetConfig, _ := base["target_config"].(map[string]any)
	var nodes []any
	hasNodes := false
	if targetConfig != nil {
		nodes, hasNodes = targetConfig["nodes"].([]any)
	}
	needsTargetConfig := targetConfig != nil || len(addedHosts) > 0
	for _, node := range sourceNodes {
		if id, ok := objectHostID(node); ok && (hasHost(addedHosts, id) || id == eventHostID) {
			needsTargetConfig = true
			break
		}
	}
	if !needsTargetConfig {
		return
	}
	if targetConfig == nil {
		targetConfig = make(map[string]any)
	}
	known := make(map[int]int, len(nodes))
	for index, node := range nodes {
		if id, ok := objectHostID(node); ok {
			known[id] = index
		}
	}
	for _, node := range sourceNodes {
		id, ok := objectHostID(node)
		if !ok || (!hasHost(addedHosts, id) && id != eventHostID) {
			continue
		}
		if index, exists := known[id]; exists {
			if sourceNode, ok := node.(map[string]any); ok {
				if currentNode, ok := nodes[index].(map[string]any); ok {
					if len(currentNode) == 1 {
						for key, value := range sourceNode {
							currentNode[key] = value
						}
						nodes[index] = currentNode
					}
				}
			}
			continue
		}
		nodes = append(nodes, node)
		known[id] = len(nodes) - 1
	}
	for _, snapshot := range snapshots {
		if !hasHost(addedHosts, snapshot.HostID) {
			continue
		}
		if _, exists := known[snapshot.HostID]; exists {
			continue
		}
		nodes = append(nodes, map[string]any{"bk_host_id": snapshot.HostID})
		known[snapshot.HostID] = len(nodes) - 1
	}
	if hasHost(addedHosts, eventHostID) {
		if _, exists := known[eventHostID]; !exists {
			nodes = append(nodes, map[string]any{"bk_host_id": eventHostID})
			known[eventHostID] = len(nodes) - 1
		}
	}
	if len(nodes) > 0 || hasNodes {
		targetConfig["nodes"] = nodes
	}
	base["target_config"] = targetConfig
}

func mergeHostPIDs(base, source map[string]any, addedHosts map[int]struct{}, eventHostID int) {
	selector, _ := base["process_selector"].(map[string]any)
	sourceSelector, _ := source["process_selector"].(map[string]any)
	var pids []any
	hasHostPIDs := false
	if selector != nil {
		pids, hasHostPIDs = selector["host_pids"].([]any)
	}
	known := make(map[int]struct{}, len(pids))
	for _, item := range pids {
		if id, ok := objectHostID(item); ok {
			known[id] = struct{}{}
		}
	}
	sourcePIDs, _ := sourceSelector["host_pids"].([]any)
	for _, item := range sourcePIDs {
		id, ok := objectHostID(item)
		if !ok || (!hasHost(addedHosts, id) && id != eventHostID) {
			continue
		}
		if _, exists := known[id]; exists {
			continue
		}
		pids = append(pids, item)
		known[id] = struct{}{}
	}
	// Event configs carry the local PID expression under config.pids. The
	// execution config is a union across hosts, so retain that expression in a
	// host-scoped item when the Event doesn't already provide host_pids.
	if selectorType, _ := sourceSelector["type"].(string); selectorType == "pid" {
		if selectorConfig, ok := sourceSelector["config"].(map[string]any); ok {
			if hostPIDs, exists := selectorConfig["pids"]; exists {
				if _, alreadyMerged := known[eventHostID]; !alreadyMerged {
					pids = append(pids, map[string]any{"bk_host_id": eventHostID, "pids": hostPIDs})
					known[eventHostID] = struct{}{}
				}
			}
		}
	}
	if len(pids) > 0 || hasHostPIDs {
		if selector == nil {
			selector = make(map[string]any)
		}
		selector["host_pids"] = pids
		base["process_selector"] = selector
	}
}

func objectHostID(value any) (int, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	rawID, exists := object["bk_host_id"]
	if !exists {
		rawID = object["host_id"]
	}
	switch id := rawID.(type) {
	case json.Number:
		parsed, err := id.Int64()
		return int(parsed), err == nil && parsed > 0 && int64(int(parsed)) == parsed
	case float64:
		parsed := int(id)
		return parsed, float64(parsed) == id && parsed > 0
	case string:
		parsed, err := json.Number(id).Int64()
		return int(parsed), err == nil && parsed > 0 && int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func hasHost(hosts map[int]struct{}, id int) bool {
	_, ok := hosts[id]
	return ok
}

func validJSON(raw json.RawMessage, fallback string) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fallback, nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid JSON")
	}
	return string(raw), nil
}
