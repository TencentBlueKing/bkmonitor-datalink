// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package core defines the transport-independent metric report, identity, and
// committed state types shared by the processor and storage layers.
package core

// ScopeKey identifies one reporting source within a business data source.
type ScopeKey struct {
	GroupID int
	Hash    string
}

// MetadataKey identifies the current metadata of a metric in a reporting scope.
type MetadataKey struct {
	ScopeKey
	FieldName string
}

// Record is a validated report, independent of the transport message and ORM.
type Record struct {
	DataID       uint
	BizID        int
	TenantID     string
	GroupID      int
	ScopeHash    string
	ScopeType    string
	RawScope     string
	Labels       string
	FieldName    string
	OriginalName string
	OriginalType string
	MetricType   string
	Meta         string
	Fingerprint  string
	TimestampMS  int64 // Collector event timestamp in Unix milliseconds.
	HasTimestamp bool
}

// Key returns the identity whose value can change on a subsequent report.
func (r Record) Key() MetadataKey {
	return MetadataKey{ScopeKey{r.GroupID, r.ScopeHash}, r.FieldName}
}
