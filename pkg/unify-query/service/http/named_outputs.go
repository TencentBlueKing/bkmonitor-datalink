// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	uqPrometheus "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

type OutputState string

const (
	OutputStateSuccess      OutputState = "SUCCESS"
	OutputStateSuccessEmpty OutputState = "SUCCESS_EMPTY"
	OutputStatePartial      OutputState = "PARTIAL"
	OutputStateError        OutputState = "ERROR"
)

type NamedOutputData struct {
	ReferenceName string           `json:"reference_name"`
	State         OutputState      `json:"state"`
	Tables        []*TablesItem    `json:"series"`
	Status        *metadata.Status `json:"status,omitempty"`
	IsPartial     bool             `json:"is_partial"`
	InvalidPoints int              `json:"invalid_points"`
}

type NamedOutputsData struct {
	ContractVersion string            `json:"contract_version"`
	Outputs         []NamedOutputData `json:"outputs"`
	Status          *metadata.Status  `json:"status,omitempty"`
	IsPartial       bool              `json:"is_partial"`
	ResultTableID   []string          `json:"result_table_id"`
	TraceID         string            `json:"trace_id,omitempty"`
}

type namedOutputSettings struct {
	MaxOutputs       int
	Timeout          time.Duration
	MaxSeries        int
	MaxPoints        int
	MaxCacheBytes    int64
	MaxResponseBytes int
}

func defaultNamedOutputSettings() namedOutputSettings {
	return namedOutputSettings{
		MaxOutputs:       4,
		Timeout:          30 * time.Second,
		MaxSeries:        10000,
		MaxPoints:        1000000,
		MaxCacheBytes:    64 * 1024 * 1024,
		MaxResponseBytes: 16 * 1024 * 1024,
	}
}

func getNamedOutputSettings() namedOutputSettings {
	settings := namedOutputSettingsSnapshot.Load()
	if settings == nil {
		return defaultNamedOutputSettings()
	}
	return *settings
}

type namedOutputExecutor func(context.Context, structured.QueryOutput) (any, bool, error)

func compileAndExecuteNamedOutput(
	ctx context.Context,
	compile func() (fmt.Stringer, error),
	execute func(context.Context, string) (any, bool, error),
) (any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	expression, err := compile()
	if err != nil {
		return nil, false, err
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	statement := expression.String()
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	return execute(ctx, statement)
}

func recordNamedOutputState(ctx context.Context, state OutputState) {
	switch state {
	case OutputStateSuccess:
		metric.NamedOutputsOutputStateInc(ctx, metric.NamedOutputsOutputSuccess)
	case OutputStateSuccessEmpty:
		metric.NamedOutputsOutputStateInc(ctx, metric.NamedOutputsOutputSuccessEmpty)
	case OutputStatePartial:
		metric.NamedOutputsOutputStateInc(ctx, metric.NamedOutputsOutputPartial)
	case OutputStateError:
		metric.NamedOutputsOutputStateInc(ctx, metric.NamedOutputsOutputError)
	}
}

func namedOutputsValidationRejectReason(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "unsupported response_contract"):
		return metric.NamedOutputsRejectUnsupportedContract
	case strings.Contains(message, "output_list length"):
		return metric.NamedOutputsRejectOutputLimit
	default:
		return metric.NamedOutputsRejectValidation
	}
}

type namedOutputLimitError struct {
	name string
	max  int
}

func (e *namedOutputLimitError) Error() string {
	return fmt.Sprintf("named outputs exceed %s: %d", e.name, e.max)
}

type namedOutputBudget struct {
	settings        namedOutputSettings
	series          int
	points          int
	responsePayload int
}

func newNamedOutputBudget(settings namedOutputSettings) *namedOutputBudget {
	return &namedOutputBudget{settings: settings}
}

func (b *namedOutputBudget) reserveSeries(ctx context.Context, responsePayload int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.series++
	if b.settings.MaxSeries > 0 && b.series > b.settings.MaxSeries {
		return &namedOutputLimitError{name: "max_series", max: b.settings.MaxSeries}
	}
	return b.reserveResponsePayload(ctx, responsePayload)
}

func (b *namedOutputBudget) reservePoint(ctx context.Context, responsePayload int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.points++
	if b.settings.MaxPoints > 0 && b.points > b.settings.MaxPoints {
		return &namedOutputLimitError{name: "max_points", max: b.settings.MaxPoints}
	}
	return b.reserveResponsePayload(ctx, responsePayload)
}

func (b *namedOutputBudget) reserveResponsePayload(ctx context.Context, bytes int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.responsePayload += bytes
	if b.settings.MaxResponseBytes > 0 && b.responsePayload > b.settings.MaxResponseBytes {
		return &namedOutputLimitError{name: "max_response_bytes", max: b.settings.MaxResponseBytes}
	}
	return nil
}

func namedPointResponseBytes(point promPromql.Point) int {
	return len(strconv.FormatInt(point.T, 10)) + len(strconv.FormatFloat(point.V, 'g', -1, 64)) + 3
}

func namedMetricResponseBytes(metric labels.Labels) int {
	bytes := 0
	for _, label := range metric {
		bytes += len(label.Name) + len(label.Value) + 6
	}
	return bytes
}

func namedResultToPromData(
	result any,
	resultColumns []string,
	decodeFunc func(string) string,
	orderBy structured.OrderBy,
) (*PromData, int, int, int, error) {
	return namedResultToPromDataWithBudget(
		context.Background(), result, resultColumns, decodeFunc, orderBy, newNamedOutputBudget(namedOutputSettings{}),
	)
}

func namedResultToPromDataWithBudget(
	ctx context.Context,
	result any,
	resultColumns []string,
	decodeFunc func(string) string,
	orderBy structured.OrderBy,
	budget *namedOutputBudget,
) (*PromData, int, int, int, error) {
	tables := promql.NewTables()
	seriesCount := 0
	pointsCount := 0
	invalidPoints := 0

	switch value := result.(type) {
	case promPromql.Matrix:
		tableIndex := 0
		for _, series := range value {
			if err := budget.reserveSeries(ctx, namedMetricResponseBytes(series.Metric)); err != nil {
				return nil, seriesCount, pointsCount, invalidPoints, err
			}
			seriesCount++
			points := make([]promPromql.Point, 0, len(series.Points))
			for _, point := range series.Points {
				if point.H == nil && (math.IsInf(point.V, 0) || math.IsNaN(point.V)) {
					if err := budget.reservePoint(ctx, 0); err != nil {
						return nil, seriesCount, pointsCount, invalidPoints, err
					}
					invalidPoints++
					continue
				}
				if err := budget.reservePoint(ctx, namedPointResponseBytes(point)); err != nil {
					return nil, seriesCount, pointsCount, invalidPoints, err
				}
				points = append(points, point)
				pointsCount++
			}
			if len(points) == 0 {
				continue
			}
			series.Points = points
			tables.Add(promql.NewTable(tableIndex, series, decodeFunc))
			if err := ctx.Err(); err != nil {
				return nil, seriesCount, pointsCount, invalidPoints, err
			}
			tableIndex++
		}
	case promPromql.Vector:
		tableIndex := 0
		for _, sample := range value {
			if err := budget.reserveSeries(ctx, namedMetricResponseBytes(sample.Metric)); err != nil {
				return nil, seriesCount, pointsCount, invalidPoints, err
			}
			seriesCount++
			if sample.H == nil && (math.IsInf(sample.V, 0) || math.IsNaN(sample.V)) {
				if err := budget.reservePoint(ctx, 0); err != nil {
					return nil, seriesCount, pointsCount, invalidPoints, err
				}
				invalidPoints++
				continue
			}
			if err := budget.reservePoint(ctx, namedPointResponseBytes(sample.Point)); err != nil {
				return nil, seriesCount, pointsCount, invalidPoints, err
			}
			tables.Add(promql.NewTableWithSample(tableIndex, sample, decodeFunc))
			if err := ctx.Err(); err != nil {
				return nil, seriesCount, pointsCount, invalidPoints, err
			}
			tableIndex++
			pointsCount++
		}
	default:
		return nil, 0, 0, 0, fmt.Errorf("data type wrong: %T", result)
	}
	sortTablesByOrderBy(tables, orderBy)

	data := NewPromData(resultColumns)
	if err := ctx.Err(); err != nil {
		return nil, seriesCount, pointsCount, invalidPoints, err
	}
	if err := data.Fill(tables); err != nil {
		return nil, 0, 0, 0, err
	}
	return data, seriesCount, pointsCount, invalidPoints, nil
}

func marshalNamedResponse(ctx context.Context, response *NamedOutputsData) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}

func ensureNamedResponseWithinLimit(ctx context.Context, response *NamedOutputsData, maxBytes int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxBytes <= 0 {
		return nil
	}
	encoded, err := marshalNamedResponse(ctx, response)
	if err != nil {
		return err
	}
	if len(encoded) > maxBytes {
		return &namedOutputLimitError{name: "max_response_bytes", max: maxBytes}
	}
	return nil
}

func marshalNamedResponseWithinLimit(response *NamedOutputsData, maxBytes int) ([]byte, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(encoded) > maxBytes {
		return nil, &namedOutputLimitError{name: "max_response_bytes", max: maxBytes}
	}
	return encoded, nil
}

func namedOutputOrder(query *structured.QueryTs) []int {
	order := make([]int, 0, len(query.OutputList))
	for index := range query.OutputList {
		if query.OutputList[index].ReferenceName == query.LegacyOutputRef {
			order = append(order, index)
			break
		}
	}
	for index := range query.OutputList {
		if query.OutputList[index].ReferenceName != query.LegacyOutputRef {
			order = append(order, index)
		}
	}
	return order
}

func markNamedOutputsRemainingError(
	ctx context.Context,
	response *NamedOutputsData,
	executionOrder []int,
	from int,
	cause error,
) {
	for _, index := range executionOrder[from:] {
		status := &metadata.Status{Code: "ERROR", Message: cause.Error()}
		response.Outputs[index].Status = status
		response.Outputs[index].IsPartial = true
		recordNamedOutputState(ctx, OutputStateError)
	}
	response.IsPartial = true
	response.Status = &metadata.Status{Code: "ERROR", Message: cause.Error()}
}

func executeNamedOutputsWith(
	ctx context.Context,
	query *structured.QueryTs,
	settings namedOutputSettings,
	routeInfo []metadata.RouteInfo,
	traceID string,
	execute namedOutputExecutor,
) (*NamedOutputsData, error) {
	response := &NamedOutputsData{
		ContractVersion: structured.NamedOutputsV1,
		Outputs:         make([]NamedOutputData, len(query.OutputList)),
		ResultTableID:   resultTableIDFromRouteInfo(routeInfo),
		TraceID:         traceID,
	}
	for index, output := range query.OutputList {
		response.Outputs[index] = NamedOutputData{
			ReferenceName: output.ReferenceName,
			State:         OutputStateError,
			Tables:        make([]*TablesItem, 0),
		}
	}
	if err := ensureNamedResponseWithinLimit(ctx, response, settings.MaxResponseBytes); err != nil {
		return nil, err
	}

	budget := newNamedOutputBudget(settings)
	successCount := 0
	deadlineConverged := false
	executionOrder := namedOutputOrder(query)
	for position, index := range executionOrder {
		output := query.OutputList[index]
		if err := ctx.Err(); err != nil {
			markNamedOutputsRemainingError(ctx, response, executionOrder, position, err)
			deadlineConverged = true
			break
		}

		outputCtx := metadata.WithStatusScope(ctx, index)
		result, isPartial, executeErr := execute(outputCtx, output)
		if executeErr != nil {
			if err := ctx.Err(); err != nil {
				markNamedOutputsRemainingError(ctx, response, executionOrder, position, err)
				deadlineConverged = true
				break
			}
			var selectorLimit *uqPrometheus.SelectorCacheLimitError
			if errors.As(executeErr, &selectorLimit) {
				recordNamedOutputState(ctx, OutputStateError)
				return nil, executeErr
			}
			response.Outputs[index].Status = &metadata.Status{Code: "ERROR", Message: executeErr.Error()}
			response.Outputs[index].IsPartial = true
			response.IsPartial = true
			response.Status = response.Outputs[index].Status
			recordNamedOutputState(ctx, OutputStateError)
			if err := ensureNamedResponseWithinLimit(ctx, response, settings.MaxResponseBytes); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					if position+1 < len(executionOrder) {
						markNamedOutputsRemainingError(ctx, response, executionOrder, position+1, ctxErr)
					}
					deadlineConverged = true
					break
				}
				return nil, err
			}
			continue
		}

		data, _, outputPoints, invalidPoints, convertErr := namedResultToPromDataWithBudget(
			outputCtx,
			result,
			query.ResultColumns,
			metadata.GetFieldFormat(outputCtx).DecodeFunc(),
			query.OrderBy,
			budget,
		)
		if convertErr != nil {
			if err := ctx.Err(); err != nil {
				markNamedOutputsRemainingError(ctx, response, executionOrder, position, err)
				deadlineConverged = true
				break
			}
			if _, isLimit := convertErr.(*namedOutputLimitError); isLimit {
				recordNamedOutputState(ctx, OutputStateError)
				return nil, convertErr
			}
			response.Outputs[index].Status = &metadata.Status{Code: "ERROR", Message: convertErr.Error()}
			response.Outputs[index].IsPartial = true
			response.IsPartial = true
			response.Status = response.Outputs[index].Status
			recordNamedOutputState(ctx, OutputStateError)
			if err := ensureNamedResponseWithinLimit(ctx, response, settings.MaxResponseBytes); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					if position+1 < len(executionOrder) {
						markNamedOutputsRemainingError(ctx, response, executionOrder, position+1, ctxErr)
					}
					deadlineConverged = true
					break
				}
				return nil, err
			}
			continue
		}

		if ok, factor, downSampleErr := downsample.CheckDownSampleRange(
			metadata.GetQueryParams(outputCtx).Step.String(),
			query.DownSampleRange,
		); ok && downSampleErr == nil {
			data.Downsample(factor)
			if err := ctx.Err(); err != nil {
				markNamedOutputsRemainingError(ctx, response, executionOrder, position, err)
				deadlineConverged = true
				break
			}
		}

		status := metadata.GetStatus(outputCtx)
		state := OutputStateSuccess
		if outputPoints == 0 {
			state = OutputStateSuccessEmpty
		}
		if isPartial || status != nil {
			state = OutputStatePartial
			isPartial = true
			response.IsPartial = true
			if response.Status == nil {
				response.Status = status
			}
		}
		response.Outputs[index] = NamedOutputData{
			ReferenceName: output.ReferenceName,
			State:         state,
			Tables:        data.Tables,
			Status:        status,
			IsPartial:     isPartial,
			InvalidPoints: invalidPoints,
		}
		recordNamedOutputState(ctx, state)
		successCount++
		if err := ensureNamedResponseWithinLimit(ctx, response, settings.MaxResponseBytes); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && position+1 < len(executionOrder) {
				markNamedOutputsRemainingError(ctx, response, executionOrder, position+1, ctxErr)
				deadlineConverged = true
				break
			}
			return nil, err
		}
	}

	if successCount == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("all named outputs failed")
	}
	var encoded []byte
	var err error
	if deadlineConverged {
		encoded, err = marshalNamedResponseWithinLimit(response, settings.MaxResponseBytes)
	} else {
		if err = ensureNamedResponseWithinLimit(ctx, response, settings.MaxResponseBytes); err == nil {
			encoded, err = marshalNamedResponse(ctx, response)
		}
	}
	if err != nil {
		return nil, err
	}
	seriesCount := 0
	pointsCount := 0
	for _, output := range response.Outputs {
		seriesCount += len(output.Tables)
		for _, table := range output.Tables {
			pointsCount += len(table.Values)
		}
	}
	metric.NamedOutputsResultSizeObserve(ctx, seriesCount, pointsCount)
	metric.NamedOutputsResponseBytesObserve(ctx, len(encoded))
	if !deadlineConverged {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return response, nil
}
