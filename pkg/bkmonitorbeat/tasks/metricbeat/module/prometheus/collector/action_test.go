// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActionDelta(t *testing.T) {
	type Input struct {
		Metric string
		Hash   string
		Ts     int64
		Value  float64
	}
	type Output struct {
		Metric string
		Value  float64
		Ok     bool
	}

	cases := []struct {
		Input  Input
		Output Output
	}{
		{
			Input:  Input{Metric: "metric3", Hash: "1001", Ts: 1000, Value: 10},
			Output: Output{Metric: "metric3", Value: 10, Ok: true},
		},
		{
			Input:  Input{Metric: "metric4", Hash: "1001", Ts: 1000, Value: 11},
			Output: Output{Metric: "metric4", Value: 11, Ok: true},
		},
		{
			Input:  Input{Metric: "metric1", Hash: "1001", Ts: 1000, Value: 11},
			Output: Output{Metric: "metric1", Value: 0, Ok: false},
		},
		{
			Input:  Input{Metric: "metric1", Hash: "1001", Ts: 1000, Value: 22},
			Output: Output{Metric: "metric1", Value: 11, Ok: true},
		},
		{
			Input:  Input{Metric: "metric2", Hash: "1001", Ts: 1000, Value: 33},
			Output: Output{Metric: "metric2", Value: 0, Ok: false},
		},
		{
			Input:  Input{Metric: "metric2", Hash: "1001", Ts: 1000, Value: 40},
			Output: Output{Metric: "metric2", Value: 7, Ok: true},
		},
	}

	op := newActionOperator(ActionTypeDelta, nil, []string{"metric1", "metric2"})
	for _, c := range cases {
		m, v, ok := op.GetOrUpdate(c.Input.Metric, c.Input.Hash, c.Input.Ts, c.Input.Value)
		assert.Equal(t, c.Output.Metric, m)
		assert.Equal(t, c.Output.Value, v)
		assert.Equal(t, c.Output.Ok, ok)
	}
}

func TestActionRate(t *testing.T) {
	type Input struct {
		Metric string
		Hash   string
		Ts     int64
		Value  float64
	}
	type Output struct {
		Metric string
		Value  float64
		Ok     bool
	}

	cases := []struct {
		Input  Input
		Output Output
	}{
		{
			Input:  Input{Metric: "metric3", Hash: "1001", Ts: 1000, Value: 10},
			Output: Output{Metric: "metric3", Value: 10, Ok: true},
		},
		{
			Input:  Input{Metric: "metric4", Hash: "1001", Ts: 1000, Value: 11},
			Output: Output{Metric: "metric4", Value: 11, Ok: true},
		},
		{
			Input:  Input{Metric: "metric1", Hash: "1001", Ts: 1000, Value: 11},
			Output: Output{Metric: "metric1.rate", Value: 0, Ok: false},
		},
		{
			Input:  Input{Metric: "metric1", Hash: "1001", Ts: 2000, Value: 22},
			Output: Output{Metric: "metric1.rate", Value: float64(11) / float64(1000), Ok: true},
		},
		{
			Input:  Input{Metric: "metric2", Hash: "1001", Ts: 1000, Value: 33},
			Output: Output{Metric: "metric2.rate", Value: 0, Ok: false},
		},
		{
			Input:  Input{Metric: "metric2", Hash: "1001", Ts: 2000, Value: 40},
			Output: Output{Metric: "metric2.rate", Value: float64(7) / float64(1000), Ok: true},
		},
	}

	rateOpts := []ActionRate{
		{Source: "metric1", Destination: "metric1.rate"},
		{Source: "metric2", Destination: "metric2.rate"},
	}

	op := newActionOperator(ActionTypeRate, rateOpts, nil)
	for _, c := range cases {
		m, v, ok := op.GetOrUpdate(c.Input.Metric, c.Input.Hash, c.Input.Ts, c.Input.Value)
		assert.Equal(t, c.Output.Metric, m)
		assert.Equal(t, c.Output.Value, v)
		assert.Equal(t, c.Output.Ok, ok)
	}
}
