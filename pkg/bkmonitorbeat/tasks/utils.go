// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"sync"
)

// BufferBuilder :
type BufferBuilder struct {
	buffer []byte
	once   sync.Once
}

// GetBuffer :
func (b *BufferBuilder) GetBuffer(initSize int) []byte {
	b.once.Do(func() {
		b.buffer = make([]byte, initSize)
	})
	return b.buffer
}

// NewBufferBuilder :
func NewBufferBuilder() *BufferBuilder {
	return &BufferBuilder{}
}

func MatchTraces(labels map[string]string) (string, string) {
	traceID := labels["traceID"]
	if traceID == "" {
		traceID = labels["trace_id"]
	}
	spanID := labels["spanID"]
	if spanID == "" {
		spanID = labels["span_id"]
	}
	return traceID, spanID
}
