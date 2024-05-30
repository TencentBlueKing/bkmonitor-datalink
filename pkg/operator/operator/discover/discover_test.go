// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"testing"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
)

func TestForwardAddressCorrectly(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		output string
	}{
		{
			input:  "myhost:8080",
			output: "127.0.0.1:8080",
		},
		{
			input:  "http://myhost:8080",
			output: "http://127.0.0.1:8080",
		},
		{
			input:  "myhost",
			output: "127.0.0.1",
		},
		{
			input:  "127.0.1.2",
			output: "127.0.0.1",
		},
		{
			input:  "http://myhost:8080/metrics",
			output: "http://127.0.0.1:8080/metrics",
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			s, err := forwardAddress(c.input)
			assert.NoError(t, err)
			assert.Equal(t, s, c.output)
		})
	}
}

func TestForwardAddressIncorrectly(t *testing.T) {
	cases := []string{
		"myhost: 8080",
		"http:// myhost:8080",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			s, err := forwardAddress(c)
			assert.Error(t, err)
			assert.Empty(t, s)
		})
	}
}

func TestMatchSelector(t *testing.T) {
	cases := []struct {
		labels   model.LabelSet
		selector map[string]string
		matched  bool
	}{
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
				model.LabelName("label2"): model.LabelValue("value2"),
			},
			selector: map[string]string{
				"label1": "value1",
			},
			matched: true,
		},
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
				model.LabelName("label2"): model.LabelValue("value2"),
			},
			selector: map[string]string{
				"label1": "value1",
				"label2": "value2",
			},
			matched: true,
		},
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
				model.LabelName("label2"): model.LabelValue("value2"),
			},
			selector: map[string]string{
				"label1": "value1",
				"label2": "value3",
			},
			matched: false,
		},
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
			},
			selector: map[string]string{
				"label1": "value1",
				"label2": "value2",
			},
			matched: false,
		},
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
			},
			selector: map[string]string{
				"label1": "value2",
			},
			matched: false,
		},
		{
			labels: map[model.LabelName]model.LabelValue{
				model.LabelName("label1"): model.LabelValue("value1"),
			},
			selector: nil,
			matched:  true,
		},
	}

	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, c.matched, matchSelector(c.labels, c.selector))
		})
	}
}
