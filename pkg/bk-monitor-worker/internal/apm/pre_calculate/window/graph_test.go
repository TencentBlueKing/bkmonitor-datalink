// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpanGraph(t *testing.T) {
	rootSpan := Node{
		StandardSpan: StandardSpan{
			TraceId:      "445e8696539f4a07cde400e0fbbe2341",
			SpanId:       "984e737f16c14b61",
			SpanName:     "rootSpan",
			ParentSpanId: "",
			StartTime:    1698667740051599,
			EndTime:      1698667740051990,
			Kind:         4,
		},
	}
	child1with1Span := Node{
		StandardSpan: StandardSpan{
			TraceId:      "445e8696539f4a07cde400e0fbbe2341",
			SpanId:       "fc931cd577c2b8d4",
			SpanName:     "1-1",
			ParentSpanId: "984e737f16c14b61",
			StartTime:    1698667740042889,
			EndTime:      1698667740070744,
			Kind:         5,
		},
	}
	child2with1Span := Node{
		StandardSpan: StandardSpan{
			TraceId:      "445e8696539f4a07cde400e0fbbe2341",
			SpanId:       "2ef9eb548c622d19",
			SpanName:     "2-1",
			ParentSpanId: "fc931cd577c2b8d4",
			StartTime:    1698667740068658,
			EndTime:      1698667740069174,
			Kind:         4,
		},
	}
	child2with2Span := Node{
		StandardSpan: StandardSpan{
			TraceId:      "445e8696539f4a07cde400e0fbbe2341",
			SpanId:       "4b477f46b2298b0b",
			SpanName:     "2-2",
			ParentSpanId: "fc931cd577c2b8d4",
			StartTime:    1698667740069357,
			EndTime:      1698667740070155,
			Kind:         3,
		},
	}

	graph := NewDiGraph()
	graph.AddNode(rootSpan)
	graph.AddNode(child1with1Span)
	graph.AddNode(child2with1Span)
	graph.AddNode(child2with2Span)
	graph.RefreshEdges()

	nodeDegrees := graph.NodeDepths()
	assert.Equal(t, "984e737f16c14b61", nodeDegrees[0].Node.SpanId)
	assert.Equal(t, "fc931cd577c2b8d4", nodeDegrees[1].Node.SpanId)
	assert.Equal(t, "2ef9eb548c622d19", nodeDegrees[2].Node.SpanId)
	assert.Equal(t, "4b477f46b2298b0b", nodeDegrees[3].Node.SpanId)
	sort.Slice(nodeDegrees, sortNode(nodeDegrees))
	assert.Equal(t, "984e737f16c14b61", nodeDegrees[0].Node.SpanId)
	assert.Equal(t, "fc931cd577c2b8d4", nodeDegrees[1].Node.SpanId)
	assert.Equal(t, "2ef9eb548c622d19", nodeDegrees[2].Node.SpanId)
	assert.Equal(t, "4b477f46b2298b0b", nodeDegrees[3].Node.SpanId)
}
