// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type rowScanner interface{ Scan(dest ...any) error }

func encodeEvent(event domain.Event) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	return data, nil
}

func decodeEvent(data []byte) (domain.Event, error) {
	var event domain.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return domain.Event{}, fmt.Errorf("decode stored event: %w", err)
	}
	normalized, err := event.Normalize()
	if err != nil {
		return domain.Event{}, fmt.Errorf("normalize stored event: %w", err)
	}
	return normalized, nil
}

func encodeProcessing(processing store.EventProcessing) ([]byte, error) {
	normalized, err := processing.Normalize()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal event processing: %w", err)
	}
	return data, nil
}

func decodeProcessing(data []byte) (store.EventProcessing, error) {
	var processing store.EventProcessing
	if err := json.Unmarshal(data, &processing); err != nil {
		return store.EventProcessing{}, fmt.Errorf("decode event processing: %w", err)
	}
	return processing.Normalize()
}

func encodeAlert(alert domain.Alert) ([]byte, error) {
	data, err := json.Marshal(alert)
	if err != nil {
		return nil, fmt.Errorf("marshal alert: %w", err)
	}
	return data, nil
}

func decodeAlert(data []byte) (domain.Alert, error) {
	var alert domain.Alert
	if err := json.Unmarshal(data, &alert); err != nil {
		return domain.Alert{}, fmt.Errorf("decode stored alert: %w", err)
	}
	normalized, err := alert.Normalize()
	if err != nil {
		return domain.Alert{}, fmt.Errorf("normalize stored alert: %w", err)
	}
	return normalized, nil
}

func encodeAlertLog(log domain.AlertLog) ([]byte, error) {
	data, err := json.Marshal(log)
	if err != nil {
		return nil, fmt.Errorf("marshal alert log: %w", err)
	}
	return data, nil
}

func decodeAlertLog(data []byte) (domain.AlertLog, error) {
	var log domain.AlertLog
	if err := json.Unmarshal(data, &log); err != nil {
		return domain.AlertLog{}, fmt.Errorf("decode stored alert log: %w", err)
	}
	normalized, err := log.Normalize()
	if err != nil {
		return domain.AlertLog{}, fmt.Errorf("normalize stored alert log: %w", err)
	}
	return normalized, nil
}

func scanStoredEvent(scanner rowScanner) (store.StoredEvent, error) {
	var payload, processingPayload []byte
	var version uint64
	if err := scanner.Scan(&payload, &processingPayload, &version); err != nil {
		return store.StoredEvent{}, err
	}
	event, err := decodeEvent(payload)
	if err != nil {
		return store.StoredEvent{}, err
	}
	processing, err := decodeProcessing(processingPayload)
	if err != nil {
		return store.StoredEvent{}, err
	}
	stored := store.StoredEvent{Event: event, Processing: processing, Version: versionToken(version)}
	if err := stored.Validate(); err != nil {
		return store.StoredEvent{}, fmt.Errorf("validate stored event: %w", err)
	}
	return stored, nil
}

func scanStoredAlert(scanner rowScanner) (store.StoredAlert, error) {
	var payload []byte
	var version uint64
	if err := scanner.Scan(&payload, &version); err != nil {
		return store.StoredAlert{}, err
	}
	alert, err := decodeAlert(payload)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if version == 0 {
		return store.StoredAlert{}, fmt.Errorf("stored alert version must be positive")
	}
	return store.StoredAlert{Alert: alert, Version: versionToken(version)}, nil
}
