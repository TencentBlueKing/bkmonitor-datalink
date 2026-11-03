// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DecodeMessage accepts a single Event, an Event array, or a Beat data envelope.
// Events with unsupported event_name values are ignored because a DataID may
// carry event types that this materializer does not process. For arrays, valid
// Events are returned alongside an error when other items are invalid.
func DecodeMessage(payload []byte) ([]Event, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty message", ErrInvalidEvent)
	}
	if payload[0] == '"' {
		var nested string
		if err := json.Unmarshal(payload, &nested); err != nil {
			return nil, fmt.Errorf("%w: decode string envelope: %v", ErrInvalidEvent, err)
		}
		return DecodeMessage([]byte(nested))
	}
	if payload[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(payload, &items); err != nil {
			return nil, fmt.Errorf("%w: decode event array: %v", ErrInvalidEvent, err)
		}
		var events []Event
		var decodeErr error
		for i, item := range items {
			decoded, err := DecodeMessage(item)
			if err != nil && decodeErr == nil {
				decodeErr = fmt.Errorf("decode event array item %d: %w", i, err)
			}
			events = append(events, decoded...)
		}
		return events, decodeErr
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode message object: %v", ErrInvalidEvent, err)
	}
	if _, hasEventName := envelope["event_name"]; !hasEventName {
		if data, ok := envelope["data"]; ok {
			return DecodeMessage(data)
		}
	}
	event, err := decodeEvent(envelope)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}
	return []Event{*event}, nil
}

func decodeEvent(envelope map[string]json.RawMessage) (*Event, error) {
	rawName, ok := envelope["event_name"]
	if !ok {
		return nil, fmt.Errorf("%w: event_name is required", ErrInvalidEvent)
	}
	var name string
	if err := json.Unmarshal(rawName, &name); err != nil || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: event_name must be a non-empty string", ErrInvalidEvent)
	}
	if name != EventStarted && name != EventFinished {
		return nil, nil
	}

	dimensions, err := decodeStringMap(envelope["dimension"])
	if err != nil {
		return nil, fmt.Errorf("%w: decode dimensions: %v", ErrInvalidEvent, err)
	}
	contentRaw, err := eventContentRaw(envelope)
	if err != nil {
		return nil, err
	}
	var content EventContent
	if err := json.Unmarshal(contentRaw, &content); err != nil {
		return nil, fmt.Errorf("%w: decode event content: %v", ErrInvalidEvent, err)
	}
	if err := validateConfigObject(content.Config); err != nil {
		return nil, err
	}
	if content.SchemaVersion > 1 {
		return nil, fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidEvent, content.SchemaVersion)
	}
	if len(content.Processes) == 0 || bytes.Equal(bytes.TrimSpace(content.Processes), []byte("null")) {
		content.Processes = json.RawMessage("[]")
	}

	timestamp, err := decodeInt64(envelope["timestamp"])
	if err != nil {
		return nil, fmt.Errorf("%w: timestamp: %v", ErrInvalidEvent, err)
	}
	bizID, err := requiredDimensionInt(dimensions, "bk_biz_id")
	if err != nil {
		return nil, err
	}
	hostID, err := requiredDimensionInt(dimensions, "bk_host_id")
	if err != nil {
		return nil, err
	}
	taskID, err := optionalDimensionInt(dimensions, "task_id")
	if err != nil {
		return nil, fmt.Errorf("%w: task_id: %v", ErrInvalidEvent, err)
	}
	scheduledAt, err := requiredDimensionInt64(dimensions, "scheduled_at")
	if err != nil {
		return nil, err
	}
	for i, target := range content.TargetHosts {
		if target.HostID <= 0 {
			return nil, fmt.Errorf("%w: target_hosts[%d].bk_host_id must be positive", ErrInvalidEvent, i)
		}
	}

	status := dimensions["status"]
	if name == EventStarted {
		if status == "" {
			status = StatusRunning
		}
		if status != StatusRunning || content.StartedAt <= 0 {
			return nil, fmt.Errorf("%w: started event requires running status and positive started_at", ErrInvalidEvent)
		}
	} else {
		if !isTerminalStatus(status) || content.FinishedAt <= 0 || content.StartedAt <= 0 {
			return nil, fmt.Errorf("%w: finished event requires a terminal status and positive started_at/finished_at", ErrInvalidEvent)
		}
	}
	if timestamp <= 0 || dimensions["bk_tenant_id"] == "" || dimensions["schedule_type"] == "" {
		return nil, fmt.Errorf("%w: timestamp, bk_tenant_id and schedule_type are required", ErrInvalidEvent)
	}

	return &Event{
		Name:             name,
		Target:           decodeString(envelope["target"]),
		Timestamp:        timestamp,
		TenantID:         dimensions["bk_tenant_id"],
		BizID:            bizID,
		TaskID:           taskID,
		ScheduleType:     dimensions["schedule_type"],
		ScheduledAt:      scheduledAt,
		HostID:           hostID,
		TaskName:         dimensions["task_name"],
		Creator:          dimensions["creator"],
		CollectionMethod: dimensions["collection_method"],
		CollectionType:   dimensions["collection_type"],
		Status:           status,
		ProcessNames:     dimensions["process_names"],
		ProcessPIDs:      dimensions["process_pids"],
		Content:          content,
	}, nil
}

func validateConfigObject(config json.RawMessage) error {
	config = bytes.TrimSpace(config)
	if len(config) == 0 || bytes.Equal(config, []byte("null")) {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil || object == nil {
		return fmt.Errorf("%w: event config must be a JSON object", ErrInvalidEvent)
	}
	return nil
}

func eventContentRaw(envelope map[string]json.RawMessage) (json.RawMessage, error) {
	eventRaw := envelope["event"]
	if len(eventRaw) != 0 {
		var value any
		if err := json.Unmarshal(eventRaw, &value); err != nil {
			return nil, fmt.Errorf("%w: decode event envelope: %v", ErrInvalidEvent, err)
		}
		if text, ok := value.(string); ok {
			eventRaw = json.RawMessage(text)
		}
		var eventFields map[string]json.RawMessage
		if err := json.Unmarshal(eventRaw, &eventFields); err == nil {
			if content, ok := eventFields["content"]; ok {
				return unwrapJSONText(content)
			}
			if _, ok := eventFields["schema_version"]; ok {
				return eventRaw, nil
			}
		}
	}
	if content, ok := envelope["content"]; ok {
		return unwrapJSONText(content)
	}
	return nil, fmt.Errorf("%w: event.content is required", ErrInvalidEvent)
}

func unwrapJSONText(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("%w: decode event.content text: %v", ErrInvalidEvent, err)
		}
		return json.RawMessage(text), nil
	}
	return raw, nil
}

func decodeStringMap(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing dimension(s)")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(fields))
	for key, value := range fields {
		result[key] = decodeString(value)
	}
	return result, nil
}

func decodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil {
		switch v := value.(type) {
		case json.Number:
			return v.String()
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

func decodeInt64(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing value")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

func requiredDimensionInt(dimensions map[string]string, key string) (int, error) {
	value, err := requiredDimensionInt64(dimensions, key)
	if err != nil {
		return 0, err
	}
	parsed := int(value)
	if int64(parsed) != value || parsed <= 0 {
		return 0, fmt.Errorf("%w: dimension %s must be a positive integer", ErrInvalidEvent, key)
	}
	return parsed, nil
}

func requiredDimensionInt64(dimensions map[string]string, key string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(dimensions[key]), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%w: dimension %s must be a positive integer", ErrInvalidEvent, key)
	}
	return value, nil
}

func optionalDimensionInt(dimensions map[string]string, key string) (int, error) {
	text := strings.TrimSpace(dimensions[key])
	if text == "" {
		// Legacy Events without task_id belong to task 0.
		return 0, nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 || int64(int(value)) != value {
		return 0, fmt.Errorf("dimension %s must be a non-negative integer", key)
	}
	return int(value), nil
}

func isTerminalStatus(status string) bool {
	switch status {
	case "succeed", "partial_succeed", "failed", "timeout", "cancelled":
		return true
	default:
		return false
	}
}
