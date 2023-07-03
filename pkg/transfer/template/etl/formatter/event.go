// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter

import (
	"context"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func NewEventFormatter(ctx context.Context, name string) (*Processor, error) {
	rt := config.ResultTableConfigFromContext(ctx)
	rtOption := utils.NewMapHelper(rt.Option)

	pipe := config.PipelineConfigFromContext(ctx)
	pipeOption := utils.NewMapHelper(pipe.Option)

	eventDimensionIF := rtOption.MustGet(config.ResultTableOptEventDimensionList)
	_, ok := eventDimensionIF.([]interface{})
	if ok {
		rtOption.Set(config.ResultTableOptEventDimensionList, make(map[string][]string))
	} else {
		data := make(map[string][]string)
		if err := mapstructure.Decode(eventDimensionIF, &data); err != nil {
			msg := fmt.Sprintf("parse event_dimensions failed, err: %+v", err)
			return nil, errors.Wrapf(define.ErrValue, msg)
		}
		rtOption.Set(config.ResultTableOptEventDimensionList, data)
	}

	return NewProcessor(ctx, name, RecordHandlers{
		CheckTimestampPrecision(
			pipeOption.GetOrDefault(config.PipelineConfigOptTimestampPrecision,
				config.PipelineConfigOptTimestampDefaultPrecision).(string),
		),
		CleanEventContent(
			rtOption.MustGet(config.ResultTableOptEventAllowNewEvent).(bool),                 // allowNewElement
			rtOption.MustGet(config.ResultTableOptEventContentList).(map[string]interface{}), // EventContent
		),
		CleanEventDimensions(
			rtOption.MustGet(config.ResultTableOptEventAllowNewDimension).(bool),            // allowNewElement
			rtOption.MustGet(config.ResultTableOptEventDimensionList).(map[string][]string), // DimensionMap
			rtOption.MustGet(config.ResultTableOptEventDimensionMustHave).([]string),        // commonDimensions
		),
		CheckEventCommonDimensions(
			rtOption.MustGet(config.ResultTableOptEventEventMustHave).([]string),     // allowNewElement
			rtOption.MustGet(config.ResultTableOptEventDimensionMustHave).([]string), // DimensionMap
		),
		CleanElementTypes,
	}), nil
}

func init() {
	define.RegisterDataProcessor("event_v2_handler", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipe config not found")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table not found")
		}

		if define.StoreFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "store not found")
		}

		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config not found")
		}

		return NewEventFormatter(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
