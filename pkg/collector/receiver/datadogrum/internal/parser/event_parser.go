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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

// Parse parses and validates one raw Datadog RUM event.
func (p *Parser) Parse(data []byte) (model.Event, error) {
	raw, err := decodeRawEvent(data)
	if err != nil {
		return nil, err
	}
	eventType, err := raw.eventType()
	if err != nil {
		return nil, err
	}

	event, err := p.parseByType(raw, eventType)
	if err != nil {
		return nil, err
	}
	if err := p.validator().Validate(event); err != nil {
		return nil, err
	}
	event.GetCommon().Normalize()
	event.GetCommon().Raw = append(json.RawMessage(nil), bytes.TrimSpace(data)...)
	return event, nil
}

func (p *Parser) parseByType(raw rawEvent, eventType model.EventType) (model.Event, error) {
	switch eventType {
	case model.EventTypeView, model.EventTypeViewUpdate:
		return p.parseView(raw)
	case model.EventTypeAction:
		return p.parseAction(raw)
	case model.EventTypeResource:
		return p.parseResource(raw)
	case model.EventTypeError:
		return p.parseError(raw)
	case model.EventTypeLongTask:
		return p.parseLongTask(raw)
	case model.EventTypeVital:
		return p.parseVital(raw)
	default:
		return nil, model.ErrUnsupportedEventType
	}
}

func (p *Parser) parseView(raw rawEvent) (*model.ViewEvent, error) {
	var view model.View
	common, present, err := decodeCommonAndSection(raw, sectionView, &view)
	if err != nil {
		return nil, err
	}
	view.Present = present
	common.ViewContext = &view.ViewContext
	return &model.ViewEvent{
		CommonFields: common,
		View:         view,
	}, nil
}

func (p *Parser) parseAction(raw rawEvent) (*model.ActionEvent, error) {
	var action model.Action
	common, present, err := decodeCommonAndSection(raw, sectionAction, &action)
	if err != nil {
		return nil, err
	}
	action.Present = present
	return &model.ActionEvent{
		CommonFields: common,
		Action:       action,
	}, nil
}

func (p *Parser) parseResource(raw rawEvent) (*model.ResourceEvent, error) {
	var resource model.Resource
	common, present, err := decodeCommonAndSection(raw, sectionResource, &resource)
	if err != nil {
		return nil, err
	}
	resource.Present = present
	return &model.ResourceEvent{
		CommonFields: common,
		Resource:     resource,
	}, nil
}

func (p *Parser) parseError(raw rawEvent) (*model.ErrorEvent, error) {
	var rumError model.Error
	common, present, err := decodeCommonAndSection(raw, sectionError, &rumError)
	if err != nil {
		return nil, err
	}
	rumError.Present = present
	return &model.ErrorEvent{
		CommonFields: common,
		Error:        rumError,
	}, nil
}

func (p *Parser) parseLongTask(raw rawEvent) (*model.LongTaskEvent, error) {
	var longTask model.LongTask
	common, present, err := decodeCommonAndSection(raw, sectionLongTask, &longTask)
	if err != nil {
		return nil, err
	}
	longTask.Present = present
	return &model.LongTaskEvent{
		CommonFields: common,
		LongTask:     longTask,
	}, nil
}

func (p *Parser) parseVital(raw rawEvent) (*model.VitalEvent, error) {
	var vital model.Vital
	common, present, err := decodeCommonAndSection(raw, sectionVital, &vital)
	if err != nil {
		return nil, err
	}
	vital.Present = present
	return &model.VitalEvent{
		CommonFields: common,
		Vital:        vital,
	}, nil
}
