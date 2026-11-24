// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// maxProbeAnswerBytes bounds a probe's answer. It groups by nothing, so it is
// one series of a no-data span's points; anything near this is not that answer.
const maxProbeAnswerBytes = 1 << 20

// probeAnswer is the part of the provider's answer a probe reads.
type probeAnswer struct {
	Series    []responseSeries `json:"series"`
	Status    *responseStatus  `json:"status"`
	IsPartial *bool            `json:"is_partial"`
}

// ProbeSourceLiveness runs one source liveness probe and says what came back:
// its status, whether it was partial, how many points counted above zero, or
// how it failed. The caller bounds it by ctx.
func (client *Client) ProbeSourceLiveness(ctx context.Context, probe sourceliveness.Probe) sourceliveness.Answer {
	path, encoded, headers, err := buildProbeWire(probe)
	if err != nil {
		return sourceliveness.Answer{Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return sourceliveness.Answer{Err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(headerQuerySource, client.querySource)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	markReplayable(request)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return sourceliveness.Answer{Err: err, Timeout: errors.Is(err, context.DeadlineExceeded)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeAnswerBytes))
		return sourceliveness.Answer{Err: fmt.Errorf("alarmd access uq: source liveness probe answered HTTP %d", response.StatusCode)}
	}
	var decoded probeAnswer
	if err := json.NewDecoder(io.LimitReader(response.Body, maxProbeAnswerBytes)).Decode(&decoded); err != nil {
		return sourceliveness.Answer{Err: fmt.Errorf("alarmd access uq: decode source liveness probe: %w", err),
			Timeout: errors.Is(err, context.DeadlineExceeded)}
	}
	if decoded.IsPartial == nil {
		// The same contract every query is held to: an answer that does not
		// say whether it is whole cannot say the source is alive.
		return sourceliveness.Answer{Err: errors.New("alarmd access uq: source liveness probe answer without is_partial")}
	}
	answer := sourceliveness.Answer{Partial: *decoded.IsPartial}
	if decoded.Status != nil {
		answer.Status = decoded.Status.Code
	}
	for _, series := range decoded.Series {
		answer.Points += pointsAboveZero(series)
	}
	return answer
}

// pointsAboveZero counts a probe series' rows whose count is above zero: the
// value column is every column but the time.
func pointsAboveZero(series responseSeries) int {
	points := 0
	for _, row := range series.Values {
		for index, cell := range row {
			if index < len(series.Columns) && series.Columns[index] == "_time" {
				continue
			}
			var value float64
			if json.Unmarshal(cell, &value) == nil && value > 0 {
				points++
				break
			}
		}
	}
	return points
}
