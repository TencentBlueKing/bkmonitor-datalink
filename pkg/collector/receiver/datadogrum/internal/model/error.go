// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type ErrorEvent struct {
	CommonFields
	Error Error `json:"error"`
}

func (e *ErrorEvent) GetType() EventType { return EventTypeError }
func (e *ErrorEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type Error struct {
	Present        bool   `json:"-"`
	ID             string `json:"id,omitempty"`
	Type           string `json:"type,omitempty"`
	Message        string `json:"message,omitempty"`
	Stack          string `json:"stack,omitempty"`
	ComponentStack string `json:"component_stack,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	Source         string `json:"source,omitempty"`
	Handling       string `json:"handling,omitempty"`
	// File/Line/Column 为错误源码定位信息，按 1-based 归一化后由 SDK 上报。
	File   string `json:"file,omitempty"`
	Line   *int64 `json:"line,omitempty"`
	Column *int64 `json:"column,omitempty"`
	// CrossOrigin 标识跨域脚本错误（Script error.）。
	CrossOrigin *bool `json:"cross_origin,omitempty"`
	// ResourceType 为加载失败的资源元素标签名（如 SCRIPT/IMG/LINK），仅 source=resource 时出现。
	ResourceType string         `json:"resource_type,omitempty"`
	Causes       []ErrorCause   `json:"causes,omitempty"`
	CSP          *CSPReport     `json:"csp,omitempty"`
	Resource     *ErrorResource `json:"resource,omitempty"`
	DebugIDs     DebugIDs       `json:"debug_ids,omitempty"`
}

type ErrorCause struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Stack   string `json:"stack,omitempty"`
	Source  string `json:"source,omitempty"`
}

type CSPReport struct {
	BlockedURI         string `json:"blocked_uri,omitempty"`
	ColumnNumber       *int64 `json:"column_number,omitempty"`
	Disposition        string `json:"disposition,omitempty"`
	DocumentURI        string `json:"document_uri,omitempty"`
	EffectiveDirective string `json:"effective_directive,omitempty"`
	LineNumber         *int64 `json:"line_number,omitempty"`
	OriginalPolicy     string `json:"original_policy,omitempty"`
	Referrer           string `json:"referrer,omitempty"`
	SourceFile         string `json:"source_file,omitempty"`
	StatusCode         *int   `json:"status_code,omitempty"`
	ViolatedDirective  string `json:"violated_directive,omitempty"`
}

type ErrorResource struct {
	Method     string            `json:"method,omitempty"`
	StatusCode *int              `json:"status_code,omitempty"`
	URL        string            `json:"url,omitempty"`
	Provider   *ResourceProvider `json:"provider,omitempty"`
}

type DebugIDs map[string]string
