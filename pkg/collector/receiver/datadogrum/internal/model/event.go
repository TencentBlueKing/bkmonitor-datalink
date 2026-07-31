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
)

// Event is the common interface implemented by supported RUM event types.
type Event interface {
	GetType() EventType
	GetCommon() *CommonFields
}

func (*ViewEvent) DatadogRumEvent()     {}
func (*ActionEvent) DatadogRumEvent()   {}
func (*ResourceEvent) DatadogRumEvent() {}
func (*ErrorEvent) DatadogRumEvent()    {}
func (*LongTaskEvent) DatadogRumEvent() {}
func (*VitalEvent) DatadogRumEvent()    {}

func (e *ViewEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type viewEvent ViewEvent
	return marshalEvent((*viewEvent)(e), &e.CommonFields)
}

func (e *ActionEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type actionEvent ActionEvent
	return marshalEvent((*actionEvent)(e), &e.CommonFields)
}

func (e *ResourceEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type resourceEvent ResourceEvent
	return marshalEvent((*resourceEvent)(e), &e.CommonFields)
}

func (e *ErrorEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type errorEvent ErrorEvent
	return marshalEvent((*errorEvent)(e), &e.CommonFields)
}

func (e *LongTaskEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type longTaskEvent LongTaskEvent
	return marshalEvent((*longTaskEvent)(e), &e.CommonFields)
}

func (e *VitalEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	type vitalEvent VitalEvent
	return marshalEvent((*vitalEvent)(e), &e.CommonFields)
}

// marshalEvent preserves a parsed event's original JSON; manually built events
// are marshaled from typed fields and receive the shared view context.
func marshalEvent(event any, common *CommonFields) ([]byte, error) {
	if len(common.Raw) > 0 {
		return append([]byte(nil), common.Raw...), nil
	}

	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if common.ViewContext == nil {
		return data, nil
	}

	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	view, err := json.Marshal(common.ViewContext)
	if err != nil {
		return nil, err
	}
	fields["view"] = view
	return json.Marshal(fields)
}
