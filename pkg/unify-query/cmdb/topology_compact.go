// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

// CompactTopology contains independent full object versions and little-endian
// arrays of hexadecimal uint64 time-mask words. IDs are decimal strings.
// Version 1 preserves the snapshot contract through lazy per-frame decoding.
type CompactTopology struct {
	Version    int                      `json:"version"`
	Timestamps []int64                  `json:"timestamps"`
	Nodes      []CompactTopologyNode    `json:"nodes"`
	Edges      []CompactTopologyEdge    `json:"edges"`
	Partial    []CompactTopologyPartial `json:"partial"`
	// Counts describe the expanded output, not dictionary size.
	NodeOccurrences int `json:"node_occurrences"`
	EdgeOccurrences int `json:"edge_occurrences"`
}

type CompactTopologyNode struct {
	ID           string   `json:"id"`
	ResourceType Resource `json:"resource_type"`
	Dimensions   Matcher  `json:"dimensions"`
	Mask         []string `json:"mask"`
}

type CompactTopologyEdge struct {
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	RelationType string   `json:"relation_type"`
	MetricName   string   `json:"metric_name"`
	Category     string   `json:"category"`
	Direction    string   `json:"direction"`
	Mask         []string `json:"mask"`
}

type CompactTopologyPartial struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type CompactTopologyResponseData struct {
	Code           int              `json:"code"`
	StartTime      int64            `json:"start_time"`
	EndTime        int64            `json:"end_time"`
	Step           string           `json:"step"`
	PointCount     int              `json:"point_count"`
	ResponseFormat string           `json:"response_format"`
	Compact        *CompactTopology `json:"compact"`
	Message        string           `json:"message,omitempty"`
}

const CompactTopologyFormat = "compact-v1"
