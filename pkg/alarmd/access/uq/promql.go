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
	Points []RangePoint
	// Code is the provider's own classification when it returned no series.
	// Empty means the provider raised nothing.
	Code    string
	Message string
	Partial bool
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
		Columns []string    `json:"columns"`
		Values  [][]float64 `json:"values"`
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
	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.endpoint+"/query/ts/promql", bytes.NewReader(encoded))
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: build range request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(headerQuerySource, client.querySource)
	httpRequest.Header.Set(headerSpace, request.SpaceUID)

	response, err := client.doRangeRequest(ctx, httpRequest, encoded)
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: range request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return RangeResult{}, fmt.Errorf("alarmd access uq: range query returned %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, client.limits.MaxBodyBytes))
	if err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: read range response: %w", err)
	}
	var decoded promQLResponseBody
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return RangeResult{}, fmt.Errorf("alarmd access uq: decode range response: %w", err)
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
		for _, values := range series.Values {
			if timeColumn >= len(values) || valueColumn >= len(values) {
				continue
			}
			result.Points = append(result.Points, RangePoint{
				AtUnixMilli: int64(values[timeColumn]), Value: values[valueColumn],
			})
		}
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

// rangeRetryDeadline bounds one attempt.
//
// The page's own request carries no deadline -- a reader is waiting and the
// handler lets them wait -- so without this a retry could double an already
// unbounded wait. It is a ceiling on hanging, not a tuning knob.
const rangeRetryDeadline = 20 * time.Second

// doRangeRequest sends the query and retries once when the connection failed
// before any response began.
//
// A pooled keep-alive connection can be closed by the peer between the moment
// it is picked and the moment it is written to, and nothing in the client can
// see that: connections are picked most-recently-used, with no probe, and the
// peer's close is only noticed asynchronously. Go retries such a failure by
// itself for requests it considers replayable, and a POST is not one, so the
// caller receives a bare EOF instead. Observed in production: unify-query
// leaves IdleTimeout unset, which makes Go fall back to its 3s ReadTimeout for
// idle connections, while this client holds them for 90s.
//
// Retrying is safe here because a range query is a read. It is deliberately at
// this layer rather than around the group of curves above it: retrying there
// would re-run every curve to recover one.
func (client *Client) doRangeRequest(
	ctx context.Context, request *http.Request, body []byte,
) (*http.Response, error) {
	attempt := func(request *http.Request) (*http.Response, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, rangeRetryDeadline)
		// The deadline must outlive the call, so it is cancelled when the body
		// is closed rather than here.
		response, err := client.httpClient.Do(request.WithContext(attemptCtx))
		if err != nil {
			cancel()
			return nil, err
		}
		response.Body = cancelOnClose{ReadCloser: response.Body, cancel: cancel}
		return response, nil
	}
	response, err := attempt(request)
	if err == nil {
		return response, nil
	}
	// A caller that has given up is not waiting for a second try, and a
	// deadline that has already passed will not be met by repeating the work.
	if ctx.Err() != nil {
		return nil, err
	}
	client.rangeRetries.Add(1)
	// The original request's body has been consumed, so the retry gets its own
	// reader over the same bytes.
	retry, buildErr := http.NewRequestWithContext(ctx, request.Method, request.URL.String(), bytes.NewReader(body))
	if buildErr != nil {
		return nil, err
	}
	retry.Header = request.Header.Clone()
	response, retryErr := attempt(retry)
	if retryErr != nil {
		// The first error is the one reported: it describes the condition that
		// started this, and the second is usually the same thing said again.
		return nil, err
	}
	return response, nil
}

// RangeRetries is how often a range query had to be sent a second time because
// the connection failed before a response began.
//
// It is not zero on a healthy deployment -- a pooled connection closed by the
// peer is normal -- but a rate that climbs says the client and the server
// disagree about how long an idle connection lives, which is a configuration
// mismatch rather than a load problem.
func (client *Client) RangeRetries() uint64 {
	if client == nil {
		return 0
	}
	return client.rangeRetries.Load()
}

// cancelOnClose releases an attempt's deadline when the caller is done with the
// body, so the context outlives the call that created it without leaking.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body cancelOnClose) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}
