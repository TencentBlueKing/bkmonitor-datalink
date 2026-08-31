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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	clientPrometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	uqPromql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func namedOutputCounterValue(t *testing.T, familyName, labelName, labelValue string) float64 {
	t.Helper()
	families, err := clientPrometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, familyMetric := range family.GetMetric() {
			matched := false
			for _, label := range familyMetric.GetLabel() {
				if label.GetName() == labelName && label.GetValue() == labelValue {
					matched = true
					break
				}
			}
			if matched {
				return familyMetric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func namedOutputQuery() *structured.QueryTs {
	return &structured.QueryTs{
		ResponseContract: structured.NamedOutputsV1,
		LegacyOutputRef:  "C",
		OutputList: []structured.QueryOutput{
			{ReferenceName: "A", Expression: "A"},
			{ReferenceName: "B", Expression: "B"},
			{ReferenceName: "C", Expression: "A / B * 100"},
		},
	}
}

type cancelingNamedOutputStatement struct {
	cancel context.CancelFunc
}

type cancelingNamedOutputJSONValue struct {
	cancel context.CancelFunc
}

type deadlineAfterErrChecksContext struct {
	context.Context
	active  bool
	checks  int
	allowed int
}

func (c *deadlineAfterErrChecksContext) Err() error {
	if !c.active {
		return c.Context.Err()
	}
	c.checks++
	if c.checks > c.allowed {
		return context.DeadlineExceeded
	}
	return nil
}

func (v cancelingNamedOutputJSONValue) MarshalJSON() ([]byte, error) {
	v.cancel()
	return []byte("1"), nil
}

func (s cancelingNamedOutputStatement) String() string {
	s.cancel()
	return "up"
}

func TestExecuteNamedOutputsLegacyFirstResponseInRequestOrder(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetStatus(ctx, "ROUTE_PARTIAL", "base route status")
	q := namedOutputQuery()
	var calls []string

	data, err := executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "trace", func(outputCtx context.Context, output structured.QueryOutput) (any, bool, error) {
		calls = append(calls, output.ReferenceName)
		if output.ReferenceName == "B" {
			metadata.SetStatus(outputCtx, metadata.QueryTsPartial, "B partial")
		}
		return promPromql.Vector{{
			Metric: labels.FromStrings("service", "api"),
			Point:  promPromql.Point{T: 1000, V: float64(len(calls))},
		}}, output.ReferenceName == "B", nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"C", "A", "B"}, calls)
	require.Equal(t, []string{"A", "B", "C"}, []string{
		data.Outputs[0].ReferenceName,
		data.Outputs[1].ReferenceName,
		data.Outputs[2].ReferenceName,
	})
	require.Equal(t, OutputStatePartial, data.Outputs[1].State)
	require.Equal(t, metadata.QueryTsPartial, data.Outputs[1].Status.Code)
	require.Equal(t, "ROUTE_PARTIAL", data.Outputs[0].Status.Code)
	require.True(t, data.IsPartial)
	require.Equal(t, "trace", data.TraceID)
	require.Equal(t, "ROUTE_PARTIAL", metadata.GetStatus(ctx).Code, "output status must not overwrite the request status")
}

func TestNamedResultFiltersInvalidRangeAndInstantPoints(t *testing.T) {
	matrix := promPromql.Matrix{{
		Metric: labels.FromStrings("service", "api"),
		Points: []promPromql.Point{
			{T: 1000, V: 1},
			{T: 2000, V: math.NaN()},
			{T: 3000, V: math.Inf(1)},
		},
	}}
	data, series, points, invalid, err := namedResultToPromData(matrix, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, series)
	require.Equal(t, 1, points)
	require.Equal(t, 2, invalid)
	require.Len(t, data.Tables[0].Values, 1)

	vector := promPromql.Vector{
		{Metric: labels.FromStrings("service", "api"), Point: promPromql.Point{T: 1000, V: math.NaN()}},
		{Metric: labels.FromStrings("service", "web"), Point: promPromql.Point{T: 1000, V: 2}},
	}
	data, series, points, invalid, err = namedResultToPromData(vector, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 2, series)
	require.Equal(t, 1, points)
	require.Equal(t, 1, invalid)
	require.Len(t, data.Tables, 1)
}

func TestExecuteNamedOutputsEnforcesSharedDeadlineAndCapacity(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	q := namedOutputQuery()
	settings := defaultNamedOutputSettings()
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var calls int
	_, err := executeNamedOutputsWith(requestCtx, q, settings, nil, "", func(outputCtx context.Context, _ structured.QueryOutput) (any, bool, error) {
		calls++
		cancel()
		return nil, false, outputCtx.Err()
	})
	require.Error(t, err)
	require.Equal(t, 1, calls, "deadline exhaustion must not start remaining outputs")

	metadata.InitMetadata()
	ctx = metadata.InitHashID(context.Background())
	settings = defaultNamedOutputSettings()
	settings.MaxPoints = 1
	_, err = executeNamedOutputsWith(ctx, q, settings, nil, "", func(context.Context, structured.QueryOutput) (any, bool, error) {
		return promPromql.Vector{
			{Point: promPromql.Point{T: 1, V: 1}},
			{Point: promPromql.Point{T: 2, V: 2}},
		}, false, nil
	})
	require.ErrorContains(t, err, "max_points")

	metadata.InitMetadata()
	ctx = metadata.InitHashID(context.Background())
	settings = defaultNamedOutputSettings()
	settings.MaxResponseBytes = 1
	_, err = executeNamedOutputsWith(ctx, q, settings, nil, "", func(context.Context, structured.QueryOutput) (any, bool, error) {
		return promPromql.Vector{}, false, nil
	})
	require.ErrorContains(t, err, "max_response_bytes")

	metadata.InitMetadata()
	ctx = metadata.InitHashID(context.Background())
	settings = defaultNamedOutputSettings()
	settings.MaxSeries = 1
	_, err = executeNamedOutputsWith(ctx, q, settings, nil, "", func(context.Context, structured.QueryOutput) (any, bool, error) {
		return promPromql.Vector{
			{Point: promPromql.Point{T: 1, V: math.NaN()}},
			{Point: promPromql.Point{T: 2, V: math.Inf(1)}},
		}, false, nil
	})
	require.ErrorContains(t, err, "max_series")
}

func TestExecuteNamedOutputsStopsAfterDeadlineAndKeepsPriorSuccess(t *testing.T) {
	metadata.InitMetadata()
	ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
	q := namedOutputQuery()
	var calls []string
	data, err := executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "", func(outputCtx context.Context, output structured.QueryOutput) (any, bool, error) {
		calls = append(calls, output.ReferenceName)
		if output.ReferenceName == "A" {
			cancel()
			return nil, false, outputCtx.Err()
		}
		return promPromql.Vector{{Point: promPromql.Point{T: 1, V: 1}}}, false, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"C", "A"}, calls)
	require.Equal(t, OutputStateSuccess, data.Outputs[2].State)
	require.Equal(t, OutputStateError, data.Outputs[0].State)
	require.Equal(t, OutputStateError, data.Outputs[1].State)
	require.True(t, data.IsPartial)
}

func TestExecuteNamedOutputsDeadlinePartialStillEnforcesResponseLimit(t *testing.T) {
	metadata.InitMetadata()
	ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
	q := namedOutputQuery()
	initial := &NamedOutputsData{
		ContractVersion: structured.NamedOutputsV1,
		Outputs:         make([]NamedOutputData, len(q.OutputList)),
	}
	for index, output := range q.OutputList {
		initial.Outputs[index] = NamedOutputData{
			ReferenceName: output.ReferenceName,
			State:         OutputStateError,
			Tables:        make([]*TablesItem, 0),
		}
	}
	encodedInitial, err := json.Marshal(initial)
	require.NoError(t, err)
	settings := defaultNamedOutputSettings()
	settings.MaxResponseBytes = len(encodedInitial) + 1

	_, err = executeNamedOutputsWith(ctx, q, settings, nil, "", func(outputCtx context.Context, output structured.QueryOutput) (any, bool, error) {
		if output.ReferenceName == "A" {
			cancel()
			return nil, false, outputCtx.Err()
		}
		return promPromql.Vector{{Point: promPromql.Point{T: 1, V: 1}}}, false, nil
	})
	require.ErrorContains(t, err, "max_response_bytes")
}

func TestExecuteNamedOutputsMarksRemainingWhenDeadlineExpiresMarshalingOutputError(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		executionError bool
	}{
		{name: "execute error", executionError: true},
		{name: "convert error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			metadata.InitMetadata()
			deadlineCtx := &deadlineAfterErrChecksContext{Context: context.Background(), allowed: 3}
			ctx := metadata.InitHashID(deadlineCtx)
			q := namedOutputQuery()

			data, err := executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "", func(_ context.Context, output structured.QueryOutput) (any, bool, error) {
				if output.ReferenceName != "A" {
					return promPromql.Vector{{Point: promPromql.Point{T: 1, V: 1}}}, false, nil
				}
				deadlineCtx.active = true
				if testCase.executionError {
					return nil, false, errors.New("A failed")
				}
				return struct{}{}, false, nil
			})

			require.NoError(t, err)
			require.Equal(t, OutputStateSuccess, data.Outputs[2].State)
			require.Equal(t, OutputStateError, data.Outputs[0].State)
			require.True(t, data.Outputs[0].IsPartial)
			require.Equal(t, OutputStateError, data.Outputs[1].State)
			require.True(t, data.Outputs[1].IsPartial)
			require.Equal(t, "ERROR", data.Outputs[1].Status.Code)
			require.Equal(t, context.DeadlineExceeded.Error(), data.Outputs[1].Status.Message)
			require.True(t, data.IsPartial)
		})
	}
}

func TestCompileNamedOutputDoesNotCallDownstreamAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	downstreamCalls := 0
	_, _, err := compileAndExecuteNamedOutput(
		ctx,
		func() (fmt.Stringer, error) {
			return cancelingNamedOutputStatement{cancel: cancel}, nil
		},
		func(context.Context, string) (any, bool, error) {
			downstreamCalls++
			return promPromql.Vector{}, false, nil
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, downstreamCalls)
}

func TestNamedResponseMarshalDoesNotSucceedAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	response := &NamedOutputsData{
		ContractVersion: structured.NamedOutputsV1,
		Outputs: []NamedOutputData{{
			ReferenceName: "A",
			State:         OutputStateSuccess,
			Tables: []*TablesItem{{
				Values: [][]any{{cancelingNamedOutputJSONValue{cancel: cancel}}},
			}},
		}},
	}

	err := ensureNamedResponseWithinLimit(ctx, response, 1024)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNamedResultConstructionStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	decodeCalls := 0
	matrix := make(promPromql.Matrix, 100)
	for index := range matrix {
		matrix[index] = promPromql.Series{
			Metric: labels.FromStrings("service", "api"),
			Points: []promPromql.Point{{T: int64(index), V: float64(index)}},
		}
	}
	budget := newNamedOutputBudget(defaultNamedOutputSettings())
	_, _, _, _, err := namedResultToPromDataWithBudget(
		ctx,
		matrix,
		nil,
		func(value string) string {
			decodeCalls++
			cancel()
			return value
		},
		nil,
		budget,
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, decodeCalls, "cancellation must stop output materialization")

	settings := defaultNamedOutputSettings()
	settings.MaxResponseBytes = 30
	decodeCalls = 0
	_, _, _, _, err = namedResultToPromDataWithBudget(
		context.Background(),
		matrix,
		nil,
		func(value string) string {
			decodeCalls++
			return value
		},
		nil,
		newNamedOutputBudget(settings),
	)
	require.ErrorContains(t, err, "max_response_bytes")
	require.Equal(t, 1, decodeCalls, "response byte budget must stop output materialization incrementally")
}

func TestExecuteNamedOutputsKeepsPerOutputErrorsAndRejectsAllError(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	q := namedOutputQuery()
	data, err := executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "", func(_ context.Context, output structured.QueryOutput) (any, bool, error) {
		if output.ReferenceName == "A" {
			return nil, false, errors.New("A failed")
		}
		return promPromql.Vector{}, false, nil
	})
	require.NoError(t, err)
	require.Equal(t, OutputStateError, data.Outputs[0].State)
	require.Equal(t, OutputStateSuccessEmpty, data.Outputs[1].State)
	require.True(t, data.IsPartial)

	metadata.InitMetadata()
	ctx = metadata.InitHashID(context.Background())
	_, err = executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "", func(context.Context, structured.QueryOutput) (any, bool, error) {
		return nil, false, errors.New("failed")
	})
	require.ErrorContains(t, err, "all named outputs failed")
}

func TestExecuteNamedOutputsAggregatesErrorBeforePartialStatus(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	q := namedOutputQuery()
	data, err := executeNamedOutputsWith(ctx, q, defaultNamedOutputSettings(), nil, "", func(outputCtx context.Context, output structured.QueryOutput) (any, bool, error) {
		switch output.ReferenceName {
		case "C":
			metadata.SetStatus(outputCtx, metadata.QueryTsPartial, "C partial")
			return promPromql.Vector{{Point: promPromql.Point{T: 1, V: 1}}}, true, nil
		case "A":
			return nil, false, errors.New("A failed")
		default:
			return promPromql.Vector{{Point: promPromql.Point{T: 1, V: 1}}}, false, nil
		}
	})
	require.NoError(t, err)
	require.Equal(t, "ERROR", data.Status.Code)
}

func TestHandlerQueryTsRejectsUnknownNamedOutputContractBeforeQueryIO(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	body := []byte(`{
		"query_list":[{"reference_name":"A"}],
		"metric_merge":"A",
		"response_contract":"named_outputs/v2",
		"legacy_output_ref":"A",
		"output_list":[{"reference_name":"A","expression":"A"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/query/ts", bytes.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = req

	HandlerQueryTs(ginContext)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var response ErrResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Contains(t, response.Err, "unsupported response_contract")
}

func TestHandlerQueryTsRecordsNamedOutputDataSourceValidationRejection(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	body := []byte(`{
		"query_list":[{"data_source":"bklog","reference_name":"A"}],
		"metric_merge":"A",
		"response_contract":"named_outputs/v1",
		"legacy_output_ref":"A",
		"output_list":[{"reference_name":"A","expression":"A"}]
	}`)
	receivedBefore := namedOutputCounterValue(t, "unify_query_named_outputs_requests_total", "result", metric.NamedOutputsRequestReceived)
	errorBefore := namedOutputCounterValue(t, "unify_query_named_outputs_requests_total", "result", metric.NamedOutputsRequestError)
	rejectBefore := namedOutputCounterValue(t, "unify_query_named_outputs_rejections_total", "reason", metric.NamedOutputsRejectValidation)

	req := httptest.NewRequest(http.MethodPost, "/query/ts", bytes.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = req

	HandlerQueryTs(ginContext)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, receivedBefore+1, namedOutputCounterValue(t, "unify_query_named_outputs_requests_total", "result", metric.NamedOutputsRequestReceived))
	require.Equal(t, errorBefore+1, namedOutputCounterValue(t, "unify_query_named_outputs_requests_total", "result", metric.NamedOutputsRequestError))
	require.Equal(t, rejectBefore+1, namedOutputCounterValue(t, "unify_query_named_outputs_rejections_total", "reason", metric.NamedOutputsRejectValidation))
}

func TestOldServerShapeIgnoresNewRequestFieldsAndKeepsMetricMerge(t *testing.T) {
	type legacyQueryTs struct {
		MetricMerge string `json:"metric_merge"`
	}
	var decoded legacyQueryTs
	err := json.Unmarshal([]byte(`{
		"metric_merge":"A/B*100",
		"response_contract":"named_outputs/v1",
		"legacy_output_ref":"C",
		"output_list":[{"reference_name":"C","expression":"A/B*100"}]
	}`), &decoded)
	require.NoError(t, err)
	require.Equal(t, "A/B*100", decoded.MetricMerge)
}

func TestHandlerQueryTsReturnsNamedOutputsV1ForConstantInstantQuery(t *testing.T) {
	metadata.InitMetadata()
	uqPromql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	body := []byte(`{
		"metric_merge":"vector(1)",
		"response_contract":"named_outputs/v1",
		"legacy_output_ref":"C",
		"output_list":[{"reference_name":"C","expression":"vector(1)"}],
		"start_time":"1717027200",
		"end_time":"1717027200",
		"step":"60s",
		"instant":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/query/ts", bytes.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = req

	HandlerQueryTs(ginContext)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		ContractVersion string `json:"contract_version"`
		Outputs         []struct {
			ReferenceName string            `json:"reference_name"`
			State         OutputState       `json:"state"`
			Tables        []json.RawMessage `json:"series"`
		} `json:"outputs"`
		ResultTableID []string `json:"result_table_id"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, structured.NamedOutputsV1, response.ContractVersion)
	require.Len(t, response.Outputs, 1)
	require.Equal(t, "C", response.Outputs[0].ReferenceName)
	require.Equal(t, OutputStateSuccess, response.Outputs[0].State)
	require.Len(t, response.Outputs[0].Tables, 1)
	require.Empty(t, response.ResultTableID)
}

func TestHandlerQueryTsWithoutContractKeepsLegacyResponseShape(t *testing.T) {
	metadata.InitMetadata()
	uqPromql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	body := []byte(`{
		"metric_merge":"vector(1)",
		"start_time":"1717027200",
		"end_time":"1717027200",
		"step":"60s",
		"instant":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/query/ts", bytes.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = req

	HandlerQueryTs(ginContext)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Contains(t, response, "series")
	require.NotContains(t, response, "contract_version")
	require.NotContains(t, response, "outputs")
}
