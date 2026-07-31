// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

import (
	"bytes"
	"encoding/json"
)

const (
	internalFieldFormatVersion                    = "format_version"
	internalFieldDocumentVersion                  = "document_version"
	internalFieldTraceID                          = "trace_id"
	internalFieldSpanID                           = "span_id"
	internalFieldDrift                            = "drift"
	internalFieldConfiguration                    = "configuration"
	internalFieldPageStates                       = "page_states"
	internalFieldReplayStats                      = "replay_stats"
	internalFieldAction                           = "action"
	internalFieldVital                            = "vital"
	internalFieldDebugIDs                         = "debug_ids"
	internalFieldBrowserSDKVersion                = "browser_sdk_version"
	internalFieldSDKName                          = "sdk_name"
	internalFieldProfilingSampleRate              = "profiling_sample_rate"
	internalFieldTraceSampleRate                  = "trace_sample_rate"
	internalFieldStartSessionReplayRecordManually = "start_session_replay_recording_manually"
	internalFieldRemoteConfigurationID            = "remote_configuration_id"
	internalFieldCLSDevicePixelRatio              = "cls_device_pixel_ratio"
)

var internalKnownFields = []string{
	internalFieldFormatVersion,
	internalFieldDocumentVersion,
	internalFieldTraceID,
	internalFieldSpanID,
	internalFieldDrift,
	internalFieldSDKName,
	internalFieldBrowserSDKVersion,
	internalFieldProfilingSampleRate,
	internalFieldTraceSampleRate,
	internalFieldStartSessionReplayRecordManually,
	internalFieldRemoteConfigurationID,
	internalFieldCLSDevicePixelRatio,
	internalFieldConfiguration,
	internalFieldPageStates,
	internalFieldReplayStats,
	internalFieldAction,
	internalFieldVital,
	internalFieldDebugIDs,
}

// InternalFields contains Datadog's internal _dd protocol fields.
type InternalFields struct {
	FormatVersion                       *int                       `json:"format_version,omitempty"`
	DocumentVersion                     *int                       `json:"document_version,omitempty"`
	TraceID                             string                     `json:"trace_id,omitempty"`
	SpanID                              string                     `json:"span_id,omitempty"`
	Drift                               *int64                     `json:"drift,omitempty"`
	SDKName                             string                     `json:"sdk_name,omitempty"`
	BrowserSDKVersion                   string                     `json:"browser_sdk_version,omitempty"`
	ProfilingSampleRate                 *float64                   `json:"profiling_sample_rate,omitempty"`
	TraceSampleRate                     *float64                   `json:"trace_sample_rate,omitempty"`
	StartSessionReplayRecordingManually *bool                      `json:"start_session_replay_recording_manually,omitempty"`
	RemoteConfigurationID               string                     `json:"remote_configuration_id,omitempty"`
	CLSDevicePixelRatio                 *float64                   `json:"cls_device_pixel_ratio,omitempty"`
	Configuration                       *Configuration             `json:"configuration,omitempty"`
	PageStates                          []PageState                `json:"page_states,omitempty"`
	ReplayStats                         *ReplayStats               `json:"replay_stats,omitempty"`
	Action                              *ActionInternal            `json:"action,omitempty"`
	Vital                               *VitalInternal             `json:"vital,omitempty"`
	DebugIDs                            json.RawMessage            `json:"debug_ids,omitempty"`
	Additional                          map[string]json.RawMessage `json:"-"`
}

type Configuration struct {
	SessionSampleRate       *float64 `json:"session_sample_rate,omitempty"`
	SessionReplaySampleRate *float64 `json:"session_replay_sample_rate,omitempty"`
	TraceSampleRate         *float64 `json:"trace_sample_rate,omitempty"`
	ProfilingSampleRate     *float64 `json:"profiling_sample_rate,omitempty"`
}

type ReplayStats struct {
	RecordsCount         *int64 `json:"records_count,omitempty"`
	SegmentsCount        *int64 `json:"segments_count,omitempty"`
	SegmentsTotalRawSize *int64 `json:"segments_total_raw_size,omitempty"`
}

func (f InternalFields) MarshalJSON() ([]byte, error) {
	type alias InternalFields
	if len(f.Additional) == 0 {
		return json.Marshal(alias(f))
	}

	fields := make(map[string]json.RawMessage, len(internalKnownFields)+len(f.Additional))
	knownFields := []struct {
		key     string
		value   any
		include bool
	}{
		{internalFieldFormatVersion, f.FormatVersion, f.FormatVersion != nil},
		{internalFieldDocumentVersion, f.DocumentVersion, f.DocumentVersion != nil},
		{internalFieldTraceID, f.TraceID, f.TraceID != ""},
		{internalFieldSpanID, f.SpanID, f.SpanID != ""},
		{internalFieldDrift, f.Drift, f.Drift != nil},
		{internalFieldSDKName, f.SDKName, f.SDKName != ""},
		{internalFieldBrowserSDKVersion, f.BrowserSDKVersion, f.BrowserSDKVersion != ""},
		{internalFieldProfilingSampleRate, f.ProfilingSampleRate, f.ProfilingSampleRate != nil},
		{internalFieldTraceSampleRate, f.TraceSampleRate, f.TraceSampleRate != nil},
		{internalFieldStartSessionReplayRecordManually, f.StartSessionReplayRecordingManually, f.StartSessionReplayRecordingManually != nil},
		{internalFieldRemoteConfigurationID, f.RemoteConfigurationID, f.RemoteConfigurationID != ""},
		{internalFieldCLSDevicePixelRatio, f.CLSDevicePixelRatio, f.CLSDevicePixelRatio != nil},
		{internalFieldConfiguration, f.Configuration, f.Configuration != nil},
		{internalFieldPageStates, f.PageStates, len(f.PageStates) > 0},
		{internalFieldReplayStats, f.ReplayStats, f.ReplayStats != nil},
		{internalFieldAction, f.Action, f.Action != nil},
		{internalFieldVital, f.Vital, f.Vital != nil},
		{internalFieldDebugIDs, f.DebugIDs, len(f.DebugIDs) > 0},
	}
	for _, field := range knownFields {
		if !field.include {
			continue
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		fields[field.key] = value
	}
	for key, value := range f.Additional {
		fields[key] = value
	}
	return json.Marshal(fields)
}

func (f *InternalFields) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var dst InternalFields
	fields := []internalRawField{
		{key: internalFieldFormatVersion, dst: &dst.FormatVersion},
		{key: internalFieldDocumentVersion, dst: &dst.DocumentVersion},
		{key: internalFieldTraceID, dst: &dst.TraceID},
		{key: internalFieldSpanID, dst: &dst.SpanID},
		{key: internalFieldDrift, dst: &dst.Drift},
		{key: internalFieldSDKName, dst: &dst.SDKName},
		{key: internalFieldBrowserSDKVersion, dst: &dst.BrowserSDKVersion},
		{key: internalFieldProfilingSampleRate, dst: &dst.ProfilingSampleRate},
		{key: internalFieldTraceSampleRate, dst: &dst.TraceSampleRate},
		{key: internalFieldStartSessionReplayRecordManually, dst: &dst.StartSessionReplayRecordingManually},
		{key: internalFieldRemoteConfigurationID, dst: &dst.RemoteConfigurationID},
		{key: internalFieldCLSDevicePixelRatio, dst: &dst.CLSDevicePixelRatio},
		{key: internalFieldConfiguration, dst: &dst.Configuration},
		{key: internalFieldPageStates, dst: &dst.PageStates},
		{key: internalFieldReplayStats, dst: &dst.ReplayStats},
		{key: internalFieldAction, dst: &dst.Action},
		{key: internalFieldVital, dst: &dst.Vital},
		{key: internalFieldDebugIDs, dst: &dst.DebugIDs},
	}
	if err := decodeInternalFields(raw, fields); err != nil {
		return err
	}
	dst.Additional = collectUnknownFields(raw, internalKnownFields...)
	*f = dst
	return nil
}

type internalRawField struct {
	key string
	dst any
}

func decodeInternalFields(raw map[string]json.RawMessage, fields []internalRawField) error {
	for _, field := range fields {
		if err := decodeInternalField(raw, field.key, field.dst); err != nil {
			return err
		}
	}
	return nil
}

func decodeInternalField(raw map[string]json.RawMessage, key string, dst any) error {
	msg, ok := raw[key]
	if !ok || bytes.Equal(bytes.TrimSpace(msg), []byte("null")) {
		return nil
	}
	return json.Unmarshal(msg, dst)
}

// collectUnknownFields removes known _dd fields and returns the remaining raw fields.
func collectUnknownFields(raw map[string]json.RawMessage, known ...string) map[string]json.RawMessage {
	for _, key := range known {
		delete(raw, key)
	}
	if len(raw) == 0 {
		return nil
	}
	return raw
}
