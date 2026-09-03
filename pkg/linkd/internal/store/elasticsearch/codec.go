// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/store"
)

const versionPrefix = "elasticsearch:"

type eventDocument struct {
	domain.Event
	Processing store.EventProcessing `json:"processing"`
}

type alertDocument struct {
	domain.Alert
}

type alertLogDocument struct {
	domain.AlertLog
}

type searchHit struct {
	Index       string            `json:"_index"`
	ID          string            `json:"_id"`
	SeqNo       int64             `json:"_seq_no"`
	PrimaryTerm int64             `json:"_primary_term"`
	Source      json.RawMessage   `json:"_source"`
	Sort        []json.RawMessage `json:"sort"`
}

type searchResponse struct {
	PITID string `json:"pit_id"`
	Hits  struct {
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
}

type indexResponse struct {
	Index       string `json:"_index"`
	ID          string `json:"_id"`
	SeqNo       int64  `json:"_seq_no"`
	PrimaryTerm int64  `json:"_primary_term"`
}

type versionPayload struct {
	Index       string `json:"index"`
	DocumentID  string `json:"document_id"`
	SeqNo       int64  `json:"seq_no"`
	PrimaryTerm int64  `json:"primary_term"`
}

func encodeVersion(payload versionPayload) (store.VersionToken, error) {
	if err := validateTarget("version index", payload.Index); err != nil {
		return store.VersionToken{}, err
	}
	if payload.DocumentID == "" || payload.SeqNo < 0 || payload.PrimaryTerm < 1 {
		return store.VersionToken{}, fmt.Errorf("invalid elasticsearch version payload")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return store.VersionToken{}, err
	}
	return store.NewVersionToken(versionPrefix + base64.RawURLEncoding.EncodeToString(data)), nil
}

func decodeVersion(token store.VersionToken) (versionPayload, bool) {
	value := token.String()
	if len(value) <= len(versionPrefix) || value[:len(versionPrefix)] != versionPrefix {
		return versionPayload{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(value[len(versionPrefix):])
	if err != nil {
		return versionPayload{}, false
	}
	var payload versionPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return versionPayload{}, false
	}
	if validateTarget("version index", payload.Index) != nil || payload.DocumentID == "" || payload.SeqNo < 0 || payload.PrimaryTerm < 1 {
		return versionPayload{}, false
	}
	return payload, true
}

func documentID(tenantID, entityID string) string {
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tenantID)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(tenantID))
	binary.BigEndian.PutUint64(length[:], uint64(len(entityID)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(entityID))
	return hex.EncodeToString(hash.Sum(nil))
}

func alertDocumentID(alert domain.Alert) string {
	return documentID(alert.BKTenantID, alert.AlertID)
}

func encodeEventDocument(event domain.Event, processing store.EventProcessing) ([]byte, error) {
	var err error
	processing, err = processing.Normalize()
	if err != nil {
		return nil, err
	}
	return json.Marshal(eventDocument{Event: event, Processing: processing})
}

func decodeEventHit(hit searchHit) (store.StoredEvent, error) {
	var document eventDocument
	if err := json.Unmarshal(hit.Source, &document); err != nil {
		return store.StoredEvent{}, fmt.Errorf("decode elasticsearch event: %w", err)
	}
	normalized, err := document.Normalize()
	if err != nil {
		return store.StoredEvent{}, err
	}
	processing, err := document.Processing.Normalize()
	if err != nil {
		return store.StoredEvent{}, err
	}
	if hit.ID != documentID(normalized.BKTenantID, normalized.EventID) {
		return store.StoredEvent{}, fmt.Errorf("elasticsearch event document ID does not match its identity")
	}
	version, err := encodeVersion(versionPayload{Index: hit.Index, DocumentID: hit.ID, SeqNo: hit.SeqNo, PrimaryTerm: hit.PrimaryTerm})
	if err != nil {
		return store.StoredEvent{}, err
	}
	stored := store.StoredEvent{Event: normalized, Processing: processing, Version: version}
	if err := stored.Validate(); err != nil {
		return store.StoredEvent{}, err
	}
	return stored, nil
}

func encodeAlertDocument(alert domain.Alert) ([]byte, error) {
	return json.Marshal(alertDocument{Alert: alert})
}

func decodeAlertHit(hit searchHit) (store.StoredAlert, error) {
	var document alertDocument
	if err := json.Unmarshal(hit.Source, &document); err != nil {
		return store.StoredAlert{}, err
	}
	normalized, err := document.Normalize()
	if err != nil {
		return store.StoredAlert{}, err
	}
	if hit.ID != alertDocumentID(normalized) {
		return store.StoredAlert{}, fmt.Errorf("elasticsearch alert document ID does not match its identity")
	}
	version, err := encodeVersion(versionPayload{Index: hit.Index, DocumentID: hit.ID, SeqNo: hit.SeqNo, PrimaryTerm: hit.PrimaryTerm})
	if err != nil {
		return store.StoredAlert{}, err
	}
	return store.StoredAlert{Alert: normalized, Version: version}, nil
}

func encodeAlertLogDocument(log domain.AlertLog) ([]byte, error) {
	return json.Marshal(alertLogDocument{AlertLog: log})
}

func decodeAlertLogHit(hit searchHit) (domain.AlertLog, error) {
	var document alertLogDocument
	if err := json.Unmarshal(hit.Source, &document); err != nil {
		return domain.AlertLog{}, err
	}
	normalized, err := document.Normalize()
	if err != nil {
		return domain.AlertLog{}, err
	}
	if hit.ID != documentID(normalized.BKTenantID, normalized.LogID) {
		return domain.AlertLog{}, fmt.Errorf("elasticsearch alert log document ID does not match its identity")
	}
	return normalized, nil
}
