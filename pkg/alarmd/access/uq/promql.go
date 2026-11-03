// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/tracing"
	"io"
	"net/http"
	"strconv"
	"time"
)

// RangeRequest is a fixed range query over a server-owned expression.
//
// It carries no caller-supplied selectors on purpose: the expressions live in
// alarmd next to the thresholds they visualise, so a curve cannot drift from
// the judgment it is supposed to illustrate.
type RangeRequest struct {
	PromQL   string
	SpaceUID string
	Start    time.Time
	End      time.Time
	Step     time.Duration
}

// RangePoint is one sample. Time is the sample's own timestamp rather than a
// slot index, because the caller renders it against a wall clock.
type RangePoint struct {
	AtUnixMilli int64
	Value       float64
}

// RangeResult carries the samples and, when the provider answered without
// them, why.
//
// The provider reports a missing space or a missing metric as HTTP 200 with an
// empty series list and a code in the body. Read only the status line and a
// misconfigured scope is indistinguishable from a quiet system -- which is the
// worse of the two answers to get wrong, because it looks like good news.
type RangeResult struct {
	// Points is every series' samples together; Series keeps them apart,
	// each with the labels the expression grouped it by.
	Points []RangePoint
	Series []RangeSeries
	// Code is the provider's own classification when it returned no series.
	// Empty means the provider raised nothing.
	Code    string
	Message string
	Partial bool
}

// RangeSeries is one series of a range answer: its group labels, as the
// provider names them, and its samples.
type RangeSeries struct {
	Labels map[string]string
	Points []RangePoint
}

// Unavailable reports whether the provider declined to answer. An empty result
// with no code is a real "nothing happened in this window".
func (result RangeResult) Unavailable() bool { return result.Code != "" }

type promQLRequestBody struct {
	PromQL string `json:"promql"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Step   string `json:"step"`
}

type promQLResponseBody struct {
	Series []struct {
		GroupKeys   []string    `json:"group_keys"`
		GroupValues []string    `json:"group_values"`
		Columns     []string    `json:"columns"`
		Values      [][]float64 `json:"values"`
	} `json:"series"`
	Status *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	IsPartial bool `json:"is_partial"`
}

func (request RangeRequest) validate() error {
	if request.PromQL == "" || request.SpaceUID == "" {
		// The space scope is not optional in disguise: without it the provider
		// answers 200 with an empty series and SPACE_IS_NOT_EXISTS, which a page
		// would render as "nothing wrong".
		return errors.New("alarmd access uq: range query needs an expression and a space scope")
	}
	if !request.End.After(request.Start) || request.Step <= 0 {
		return errors.New("alarmd access uq: range query needs an ordered window and a positive step")
	}
	return nil
}

// Range runs one fixed expression over the same endpoint, credentials and
// response limits the evaluation path already uses.
//
// It deliberately does not go through QueryAttempt: that envelope requires a
// Slot identity and an evaluation time, and inventing one for a page refresh
// would put a query that belongs to nobody into the execution stream, where it
// would later read as an evaluation of an object that does not exist.
func (client *Client) Range(ctx context.Context, request RangeRequest) (RangeResult, error) {
	if client == nil {
		return RangeResult{}, errors.New("alarmd access uq: initialized client is required")
	}
	if err := request.validate(); err != nil {
		return RangeResult{}, err
	}
	body := promQLRequestBody{
		PromQL: request.PromQL,
		Start:  strconv.FormatInt(request.Start.Unix(), 10),
		End:    strconv.FormatInt(request.End.Unix(), 10),
		Step:   request.Step.String(),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: encode range request: %w", err)
	}
	// The page's own request carries no deadline - a reader is waiting and
	// the handler lets them wait - so the query bounds itself. It is a
	// ceiling on hanging, not a tuning knob.
	ctx, cancel := context.WithTimeout(ctx, RangeDeadline)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.endpoint+"/query/ts/promql", bytes.NewReader(encoded))
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: build range request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(headerQuerySource, client.querySource)
	httpRequest.Header.Set(headerSpace, request.SpaceUID)
	// A page's read is no Slot's: a fresh trace, not sampled.
	_, traceParent, traceID := tracing.StartQuery(ctx, "uq", 0, 1)
	httpRequest.Header.Set(headerTraceParent, traceParent)
	// A connection the server dropped while idle is re-dialed by the
	// transport, as for an evaluation query (markReplayable).
	markReplayable(httpRequest)

	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		// The trace id it was sent with, as a refusal names it, so a timeout
		// or a cut connection can be found on the provider's side too.
		return RangeResult{}, fmt.Errorf("alarmd access uq: range request (trace %s): %w", traceID, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		// The error can reach a log, so it names the status, the refusal's
		// class and the trace id to find it by on the provider's side; never
		// the body, which can echo the expression.
		body, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyRead))
		rejection := providerRejection(response.StatusCode, body, traceID)
		return RangeResult{}, fmt.Errorf("alarmd access uq: range query returned %d (%s, trace %s)",
			response.StatusCode, rejection.Class, rejection.TraceID)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, client.limits.MaxBodyBytes))
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: read range response (trace %s): %w", traceID, err)
	}
	var decoded promQLResponseBody
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: decode range response (trace %s): %w", traceID, err)
	}

	result := RangeResult{Partial: decoded.IsPartial}
	if decoded.Status != nil && decoded.Status.Code != "" {
		result.Code, result.Message = decoded.Status.Code, decoded.Status.Message
		return result, nil
	}
	for _, series := range decoded.Series {
		timeColumn, valueColumn := columnIndexes(series.Columns)
		if timeColumn < 0 || valueColumn < 0 {
			return RangeResult{}, errors.New("alarmd access uq: range response is missing the time or value column")
		}
		kept := RangeSeries{Labels: map[string]string{}, Points: []RangePoint{}}
		for index, key := range series.GroupKeys {
			if index < len(series.GroupValues) {
				kept.Labels[key] = series.GroupValues[index]
			}
		}
		for _, values := range series.Values {
			if timeColumn >= len(values) || valueColumn >= len(values) {
				continue
			}
			point := RangePoint{AtUnixMilli: int64(values[timeColumn]), Value: values[valueColumn]}
			result.Points = append(result.Points, point)
			kept.Points = append(kept.Points, point)
		}
		result.Series = append(result.Series, kept)
	}
	return result, nil
}

func columnIndexes(columns []string) (int, int) {
	timeColumn, valueColumn := -1, -1
	for index, column := range columns {
		switch column {
		case "_time":
			timeColumn = index
		case "_value":
			valueColumn = index
		}
	}
	return timeColumn, valueColumn
}

// RangeDeadline bounds one range query; a reader that runs several side by
// side (the trends) takes it as one line's share of its time.
const RangeDeadline = 20 * time.Second
