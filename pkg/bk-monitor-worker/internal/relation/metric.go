// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"bytes"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

type Label struct {
	Name  string
	Value string
}

type Labels []Label

func (l Labels) Label() []prompb.Label {
	lbs := make([]prompb.Label, 0, len(l))
	for _, i := range l {
		lbs = append(lbs, prompb.Label{
			Name:  i.Name,
			Value: i.Value,
		})
	}
	return lbs
}

type Metric struct {
	Name   string
	Labels Labels
}

func (m Metric) TimeSeries(timestamp time.Time) prompb.TimeSeries {
	lbs := append(
		[]prompb.Label{
			{
				Name:  "__name__",
				Value: m.Name,
			},
		},
		m.Labels.Label()...,
	)

	return prompb.TimeSeries{
		Labels:  lbs,
		Samples: []prompb.Sample{{Value: 1, Timestamp: timestamp.UnixMilli()}},
	}
}

func (m Metric) String(labels ...Label) string {
	var buf bytes.Buffer
	buf.WriteString(m.Name)
	buf.WriteString(`{`)

	m.Labels = append(m.Labels, labels...)

	var n int
	for _, label := range m.Labels {
		if n > 0 {
			buf.WriteString(`,`)
		}
		n++
		buf.WriteString(label.Name)
		buf.WriteString(`="`)
		buf.WriteString(label.Value)
		buf.WriteString(`"`)
	}

	buf.WriteString("} 1")
	return buf.String()
}
