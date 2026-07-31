// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package parser

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
	modelvalidator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/validator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Parser converts raw Datadog RUM events into validated model events.
type Parser struct {
	SkipInvalid    bool
	eventValidator modelvalidator.Validator
}

// New creates a parser with the requested invalid-event policy.
func New(skipInvalid bool) *Parser {
	return &Parser{SkipInvalid: skipInvalid}
}

// ParseBatch parses and validates all events in a request batch.
func (p *Parser) ParseBatch(rawEvents [][]byte) (*model.Batch, error) {
	if len(rawEvents) == 0 {
		return nil, model.ErrEmptyBody
	}

	parsedEvents := make([]model.Event, 0, len(rawEvents))
	for _, rawEvent := range rawEvents {
		event, err := p.Parse(rawEvent)
		if err != nil {
			if p.SkipInvalid {
				logger.Warnf("skip invalid datadog rum event: %s", err)
				continue
			}
			return nil, err
		}
		parsedEvents = append(parsedEvents, event)
	}
	if len(parsedEvents) == 0 {
		return nil, model.ErrEmptyBody
	}
	return &model.Batch{Events: parsedEvents}, nil
}

func (p *Parser) validator() *modelvalidator.Validator {
	return &p.eventValidator
}
