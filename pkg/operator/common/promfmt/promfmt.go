// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promfmt

import "io"

type Metric struct {
	Name   string
	Labels []Label
}

type Label struct {
	Name  string
	Value string
}

func FmtBytes(w io.Writer, metrics ...Metric) {
	for _, metric := range metrics {
		w.Write([]byte(metric.Name))
		w.Write([]byte(`{`))

		var n int
		for _, label := range metric.Labels {
			if n > 0 {
				w.Write([]byte(`,`))
			}
			n++
			w.Write([]byte(label.Name))
			w.Write([]byte(`="`))
			w.Write([]byte(label.Value))
			w.Write([]byte(`"`))
		}

		w.Write([]byte("} 1"))
		w.Write([]byte("\n"))
	}
}
