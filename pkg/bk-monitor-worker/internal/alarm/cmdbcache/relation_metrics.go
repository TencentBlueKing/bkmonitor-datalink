// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"bytes"
	"fmt"
	"github.com/prometheus/prometheus/prompb"
	"time"
)

type RelationLabel struct {
	Name  string
	Value string
}

type RelationLabelList []RelationLabel

func (l RelationLabelList) Label() []prompb.Label {
	lbs := make([]prompb.Label, 0, len(l))
	for _, i := range l {
		lbs = append(lbs, prompb.Label{
			Name:  i.Name,
			Value: i.Value,
		})
	}
	return lbs
}

type RelationMetric struct {
	Name   string
	Labels RelationLabelList
}

func (m RelationMetric) TimeSeries(bkBizID int, timestamp time.Time) prompb.TimeSeries {
	lbs := append(
		[]prompb.Label{
			{
				Name:  "__name__",
				Value: m.Name,
			},
			{
				Name:  "bk_biz_id",
				Value: fmt.Sprintf("%d", bkBizID),
			},
		},
		m.Labels.Label()...,
	)

	return prompb.TimeSeries{
		Labels:  lbs,
		Samples: []prompb.Sample{{Value: 1, Timestamp: timestamp.UnixMilli()}},
	}
}

func (m RelationMetric) String(bkBizID int) string {
	var buf bytes.Buffer
	buf.WriteString(m.Name)
	buf.WriteString(`{`)

	m.Labels = append(m.Labels, RelationLabel{
		Name:  "bk_biz_id",
		Value: fmt.Sprintf("%d", bkBizID),
	})

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
