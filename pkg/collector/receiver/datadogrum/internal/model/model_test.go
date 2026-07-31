// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventMarshalPreservesRawPayload(t *testing.T) {
	raw := json.RawMessage(`{"type":"action","view":{"id":"view"},"action":{"type":"click"},"custom":{"value":1}}`)
	event := &ActionEvent{}
	event.CommonFields.Raw = raw

	encoded, err := json.Marshal(event)
	assert.NoError(t, err)
	assert.JSONEq(t, string(raw), string(encoded))
}

func TestEventMarshalIncludesViewContext(t *testing.T) {
	event := &ActionEvent{
		CommonFields: CommonFields{
			Date:        1,
			Type:        EventTypeAction,
			Application: Application{ID: "app"},
			ViewContext: &ViewContext{ID: "view", URL: "https://example.com"},
		},
		Action: Action{Present: true, Type: "click"},
	}

	encoded, err := json.Marshal(event)
	assert.NoError(t, err)
	var payload map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(encoded, &payload))
	assert.Contains(t, payload, "view")
}

func TestInternalFieldsDebugIDsAcceptArray(t *testing.T) {
	var fields InternalFields
	err := json.Unmarshal([]byte(`{"debug_ids":[{"url":"https://example.com/a.js","id":"abc"}]}`), &fields)
	assert.NoError(t, err)
}

func TestInternalFieldsMarshalPreservesAdditional(t *testing.T) {
	traceID := "trace-1"
	fields := InternalFields{
		TraceID:    traceID,
		Additional: map[string]json.RawMessage{"custom": json.RawMessage(`{"value":1}`)},
	}

	encoded, err := json.Marshal(fields)
	assert.NoError(t, err)

	var payload map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(encoded, &payload))
	assert.Contains(t, payload, "trace_id")
	assert.Contains(t, payload, "custom")
	assert.NotContains(t, payload, "span_id")
}

func TestInternalFieldsMarshalAdditionalPreservesOmitEmpty(t *testing.T) {
	zeroInt := 0
	zeroFloat := 0.0
	zeroBool := false
	fields := InternalFields{
		FormatVersion:                       &zeroInt,
		ProfilingSampleRate:                 &zeroFloat,
		StartSessionReplayRecordingManually: &zeroBool,
		Configuration:                       &Configuration{},
		ReplayStats:                         &ReplayStats{},
		Action:                              &ActionInternal{},
		Vital:                               &VitalInternal{},
		PageStates:                          []PageState{},
		DebugIDs:                            json.RawMessage(`{"id":1}`),
		Additional: map[string]json.RawMessage{
			"trace_id":   json.RawMessage(`"overridden"`),
			"custom":     json.RawMessage(`{"value":1}`),
			"null_value": nil,
		},
	}

	encoded, err := json.Marshal(fields)
	assert.NoError(t, err)
	var payload map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(encoded, &payload))

	assert.JSONEq(t, `0`, string(payload["format_version"]))
	assert.JSONEq(t, `0`, string(payload["profiling_sample_rate"]))
	assert.JSONEq(t, `false`, string(payload["start_session_replay_recording_manually"]))
	assert.JSONEq(t, `{}`, string(payload["configuration"]))
	assert.JSONEq(t, `{}`, string(payload["replay_stats"]))
	assert.JSONEq(t, `{}`, string(payload["action"]))
	assert.JSONEq(t, `{}`, string(payload["vital"]))
	assert.JSONEq(t, `{"id":1}`, string(payload["debug_ids"]))
	assert.Equal(t, `"overridden"`, string(payload["trace_id"]))
	assert.JSONEq(t, `{"value":1}`, string(payload["custom"]))
	assert.Equal(t, "null", string(payload["null_value"]))
	assert.NotContains(t, payload, "document_version")
	assert.NotContains(t, payload, "page_states")
	assert.NotContains(t, payload, "span_id")
}

func TestInternalFieldsMarshalAdditionalReturnsRawMessageErrors(t *testing.T) {
	invalidKnown := InternalFields{
		DebugIDs:   json.RawMessage(`{`),
		Additional: map[string]json.RawMessage{"debug_ids": json.RawMessage(`[]`)},
	}
	_, err := json.Marshal(invalidKnown)
	assert.Error(t, err)

	invalidAdditional := InternalFields{
		Additional: map[string]json.RawMessage{"custom": json.RawMessage(`{`)},
	}
	_, err = json.Marshal(invalidAdditional)
	assert.Error(t, err)
}

func TestInternalFieldsRoundTrip(t *testing.T) {
	formatVersion := 2
	documentVersion := 1
	drift := int64(100)
	sessionSampleRate := 50.0
	traceSampleRate := 25.0
	startManually := true
	clsRatio := 2.0

	original := InternalFields{
		FormatVersion:                       &formatVersion,
		DocumentVersion:                     &documentVersion,
		TraceID:                             "trace-1",
		SpanID:                              "span-1",
		Drift:                               &drift,
		SDKName:                             "browser-sdk",
		BrowserSDKVersion:                   "5.0.0",
		ProfilingSampleRate:                 &traceSampleRate,
		TraceSampleRate:                     &sessionSampleRate,
		StartSessionReplayRecordingManually: &startManually,
		RemoteConfigurationID:               "remote-1",
		CLSDevicePixelRatio:                 &clsRatio,
		Configuration: &Configuration{
			SessionSampleRate:       &sessionSampleRate,
			SessionReplaySampleRate: &sessionSampleRate,
			TraceSampleRate:         &traceSampleRate,
			ProfilingSampleRate:     &traceSampleRate,
		},
	}

	encoded, err := json.Marshal(original)
	assert.NoError(t, err)

	var decoded InternalFields
	assert.NoError(t, json.Unmarshal(encoded, &decoded))

	assert.Equal(t, *original.FormatVersion, *decoded.FormatVersion)
	assert.Equal(t, *original.DocumentVersion, *decoded.DocumentVersion)
	assert.Equal(t, original.TraceID, decoded.TraceID)
	assert.Equal(t, original.SpanID, decoded.SpanID)
	assert.Equal(t, *original.Drift, *decoded.Drift)
	assert.Equal(t, original.SDKName, decoded.SDKName)
	assert.Equal(t, original.BrowserSDKVersion, decoded.BrowserSDKVersion)
	assert.Equal(t, *original.ProfilingSampleRate, *decoded.ProfilingSampleRate)
	assert.Equal(t, *original.TraceSampleRate, *decoded.TraceSampleRate)
	assert.Equal(t, *original.StartSessionReplayRecordingManually, *decoded.StartSessionReplayRecordingManually)
	assert.Equal(t, original.RemoteConfigurationID, decoded.RemoteConfigurationID)
	assert.Equal(t, *original.CLSDevicePixelRatio, *decoded.CLSDevicePixelRatio)
	assert.NotNil(t, decoded.Configuration)
	assert.Equal(t, *original.Configuration.SessionSampleRate, *decoded.Configuration.SessionSampleRate)
}

func TestInternalFieldsUnknownFields(t *testing.T) {
	payload := `{"trace_id":"trace-1","unknown_field":{"nested":true},"another_unknown":123}`
	var fields InternalFields
	assert.NoError(t, json.Unmarshal([]byte(payload), &fields))

	assert.Equal(t, "trace-1", fields.TraceID)
	assert.Contains(t, fields.Additional, "unknown_field")
	assert.Contains(t, fields.Additional, "another_unknown")
	assert.JSONEq(t, `{"nested":true}`, string(fields.Additional["unknown_field"]))
	assert.JSONEq(t, `123`, string(fields.Additional["another_unknown"]))

	encoded, err := json.Marshal(fields)
	assert.NoError(t, err)
	var roundTrip map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(encoded, &roundTrip))
	assert.Contains(t, roundTrip, "unknown_field")
	assert.Contains(t, roundTrip, "another_unknown")
}

func TestInternalFieldsNullHandling(t *testing.T) {
	payload := `{"trace_id":"trace-1","format_version":null,"drift":null,"configuration":null}`
	var fields InternalFields
	assert.NoError(t, json.Unmarshal([]byte(payload), &fields))

	assert.Equal(t, "trace-1", fields.TraceID)
	assert.Nil(t, fields.FormatVersion)
	assert.Nil(t, fields.Drift)
	assert.Nil(t, fields.Configuration)
}

func TestInternalFieldsNumericFields(t *testing.T) {
	payload := `{"format_version":2,"document_version":1,"drift":-50,"profiling_sample_rate":12.5,"trace_sample_rate":37.5,"cls_device_pixel_ratio":1.5}`
	var fields InternalFields
	assert.NoError(t, json.Unmarshal([]byte(payload), &fields))

	assert.NotNil(t, fields.FormatVersion)
	assert.Equal(t, 2, *fields.FormatVersion)
	assert.NotNil(t, fields.DocumentVersion)
	assert.Equal(t, 1, *fields.DocumentVersion)
	assert.NotNil(t, fields.Drift)
	assert.Equal(t, int64(-50), *fields.Drift)
	assert.NotNil(t, fields.ProfilingSampleRate)
	assert.InDelta(t, 12.5, *fields.ProfilingSampleRate, 0.0001)
	assert.NotNil(t, fields.TraceSampleRate)
	assert.InDelta(t, 37.5, *fields.TraceSampleRate, 0.0001)
	assert.NotNil(t, fields.CLSDevicePixelRatio)
	assert.InDelta(t, 1.5, *fields.CLSDevicePixelRatio, 0.0001)
}
