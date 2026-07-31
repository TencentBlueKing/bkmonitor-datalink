// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package parser

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

const (
	// Common event fields.
	fieldType         = "type"
	fieldDate         = "date"
	fieldTimestamp    = "timestamp"
	fieldSource       = "source"
	fieldService      = "service"
	fieldVersion      = "version"
	fieldBuildVersion = "build_version"
	fieldBuildID      = "build_id"
	fieldTags         = "ddtags"

	fieldApplication  = "application"
	fieldSession      = "session"
	fieldUser         = "usr"
	fieldAccount      = "account"
	fieldTab          = "tab"
	fieldConnectivity = "connectivity"
	fieldDisplay      = "display"
	fieldDevice       = "device"
	fieldOS           = "os"
	fieldContext      = "context"
	fieldFeatureFlags = "feature_flags"
	fieldPrivacy      = "privacy"
	fieldContainer    = "container"
	fieldStream       = "stream"
	fieldInternal     = "_dd"

	// Event-specific sections.
	sectionView     = "view"
	sectionAction   = "action"
	sectionResource = "resource"
	sectionError    = "error"
	sectionLongTask = "long_task"
	sectionVital    = "vital"
)

type rawEvent map[string]json.RawMessage

func decodeRawEvent(data []byte) (rawEvent, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, model.ErrInvalidPayload
	}

	var raw rawEvent
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, model.ErrInvalidPayload
	}
	if raw == nil {
		return nil, model.ErrInvalidPayload
	}
	return raw, nil
}

func (r rawEvent) eventType() (model.EventType, error) {
	msg, ok := r[fieldType]
	if !ok {
		return "", model.ErrMissingRequiredField
	}
	if isJSONNull(msg) {
		return "", model.ErrInvalidPayload
	}

	var eventType model.EventType
	if err := json.Unmarshal(msg, &eventType); err != nil {
		return "", model.ErrInvalidPayload
	}
	return eventType, nil
}

func decodeCommonAndSection(raw rawEvent, section string, dst any) (model.CommonFields, bool, error) {
	common, err := decodeCommon(raw)
	if err != nil {
		return common, false, err
	}

	present, err := raw.decodeField(section, dst)
	if err != nil {
		return common, false, err
	}
	return common, present, nil
}

func decodeCommon(raw rawEvent) (model.CommonFields, error) {
	var common model.CommonFields
	var err error

	decodeInto(&err, raw, fieldType, &common.Type)
	decodeInto(&err, raw, fieldDate, &common.Date)
	decodeInto(&err, raw, fieldTimestamp, &common.Timestamp)
	decodeInto(&err, raw, fieldSource, &common.Source)
	decodeInto(&err, raw, fieldService, &common.Service)
	decodeInto(&err, raw, fieldVersion, &common.Version)
	decodeInto(&err, raw, fieldBuildVersion, &common.BuildVersion)
	decodeInto(&err, raw, fieldBuildID, &common.BuildID)
	decodeInto(&err, raw, fieldTags, &common.Tags)
	decodeInto(&err, raw, fieldApplication, &common.Application)
	decodePtrInto(&err, raw, fieldSession, &common.Session)
	decodePtrInto(&err, raw, sectionView, &common.ViewContext)
	decodePtrInto(&err, raw, fieldUser, &common.User)
	decodePtrInto(&err, raw, fieldAccount, &common.Account)
	decodePtrInto(&err, raw, fieldTab, &common.Tab)
	decodePtrInto(&err, raw, fieldConnectivity, &common.Connectivity)
	decodePtrInto(&err, raw, fieldDisplay, &common.Display)
	decodePtrInto(&err, raw, fieldDevice, &common.Device)
	decodePtrInto(&err, raw, fieldOS, &common.OS)
	decodeInto(&err, raw, fieldContext, &common.Context)
	decodeInto(&err, raw, fieldFeatureFlags, &common.FeatureFlags)
	decodeInto(&err, raw, fieldPrivacy, &common.Privacy)
	decodeInto(&err, raw, fieldContainer, &common.Container)
	decodeInto(&err, raw, fieldStream, &common.Stream)
	decodePtrInto(&err, raw, fieldInternal, &common.Internal)

	if err != nil {
		return common, err
	}
	return common, nil
}

func decodeInto(err *error, raw rawEvent, key string, dst any) {
	if *err != nil {
		return
	}
	_, *err = raw.decodeField(key, dst)
}

func decodePtrInto[T any](err *error, raw rawEvent, key string, dst **T) {
	if *err != nil {
		return
	}
	*dst, *err = decodePtr[T](raw, key)
}

func decodePtr[T any](raw rawEvent, key string) (*T, error) {
	var dst T
	ok, err := raw.decodeField(key, &dst)
	if err != nil || !ok {
		return nil, err
	}
	return &dst, nil
}

func (r rawEvent) decodeField(key string, dst any) (bool, error) {
	msg, ok := r[key]
	if !ok || isJSONNull(msg) {
		return false, nil
	}
	if err := json.Unmarshal(msg, dst); err != nil {
		return true, fmt.Errorf("%w: field %s: %v", model.ErrInvalidPayload, key, err)
	}
	return true, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
