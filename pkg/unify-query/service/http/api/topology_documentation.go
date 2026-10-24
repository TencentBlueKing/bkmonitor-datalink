// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// SharedTopologyResponseDocumentation documents the existing topology wire
// response. Each query_list item chooses its own format, so data may mix formats.
// These documentation types do not participate in response encoding.
// @Description Each query_list item selects its response format independently; data can contain both snapshots and compact-v1 items.
type SharedTopologyResponseDocumentation struct {
	TraceID string                                    `json:"trace_id"`
	Data    []SharedTopologyResponseItemDocumentation `json:"data"`
}

// SharedTopologyResponseItemDocumentation describes both mutually exclusive
// response shapes because Swagger 2.0 has no oneOf. The default format returns
// snapshots and omits response_format and compact. A compact-v1 query returns
// response_format and compact and omits snapshots. Failed snapshot items contain
// snapshots: []; failed compact items retain response_format: "compact-v1" and
// compact: null. Code and message describe the per-query failure.
// @Description Snapshot items contain snapshots and omit response_format/compact. Compact items contain response_format=compact-v1 and compact and omit snapshots. Failed snapshot items contain snapshots: []; failed compact items retain response_format=compact-v1 and compact: null.
type SharedTopologyResponseItemDocumentation struct {
	Code       int    `json:"code"`
	StartTime  int64  `json:"start_time"`
	EndTime    int64  `json:"end_time"`
	Step       string `json:"step"`
	PointCount int    `json:"point_count"`
	Message    string `json:"message,omitempty"`
	// Snapshots is present only for the default/snapshots format, including failures.
	Snapshots []cmdb.SharedTopologySnapshot `json:"snapshots,omitempty"`
	// ResponseFormat is present only when compact-v1 was requested, including failures.
	ResponseFormat string `json:"response_format,omitempty" enums:"compact-v1"`
	// Compact is present only for compact-v1; it is null when that query failed.
	Compact *cmdb.CompactTopology `json:"compact,omitempty" extensions:"x-nullable"`
}
