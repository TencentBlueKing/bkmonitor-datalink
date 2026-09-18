// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

const (
	RelationBackendSurrealDB = "surrealdb"
	RelationBackendTimeGraph = "timegraph"
	RelationBackendAuto      = "auto"
)

func normalizeRelationBackend(backend string) string {
	switch backend {
	case RelationBackendTimeGraph, RelationBackendAuto:
		return backend
	default:
		return RelationBackendSurrealDB
	}
}
