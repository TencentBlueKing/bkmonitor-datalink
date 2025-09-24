// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluator

import (
	"math/rand"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
)

type samplingPriority int

const (
	// deferDecision means that the decision if a span will be "sampled" (ie.:
	// forwarded by the collector) is made by hashing the trace ID according
	// to the configured sampling rate.
	deferDecision samplingPriority = iota
	// mustSampleSpan indicates that the span had a "sampling.priority" attribute
	// greater than zero and it is going to be sampled, ie.: forwarded by the
	// collector.
	mustSampleSpan
	// doNotSampleSpan indicates that the span had a "sampling.priority" attribute
	// equal zero and it is NOT going to be sampled, ie.: it won't be forwarded
	// by the collector.
	doNotSampleSpan

	// The constants help translate user friendly percentages to numbers direct used in sampling.
	numHashBuckets        = 0x4000 // Using a power of 2 to avoid division.
	bitMaskHashBuckets    = numHashBuckets - 1
	percentageScaleFactor = numHashBuckets / 100.0
)

func newRandomEvaluator(c Config) Evaluator {
	rand.Seed(time.Now().UnixNano())
	return randomEvaluator{
		keepAll:            c.SamplingPercentage >= 100.0,
		hashSeed:           uint32(12345), // 保持固定的 seed 多实例场景下效果才能一致
		scaledSamplingRate: uint32(c.SamplingPercentage * percentageScaleFactor),
	}
}

// randomEvaluator 随机采样（概率采样）
type randomEvaluator struct {
	keepAll            bool // fastpath 全采样的场景下就无需 hash 计算了
	hashSeed           uint32
	scaledSamplingRate uint32
}

func (e randomEvaluator) Evaluate(record *define.Record) error {
	if e.keepAll {
		return nil
	}
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		record.Data = e.processTraces(pdTraces)
	}

	return nil
}

func (e randomEvaluator) Stop() {}

func (e randomEvaluator) Type() string {
	return evaluatorTypeRandom
}

func (e randomEvaluator) processTraces(pdTraces ptrace.Traces) ptrace.Traces {
	foreach.SpansRemoveIf(pdTraces, func(span ptrace.Span) bool {
		// 如果 StatusCode 是 Err 类型的则必须保留
		if span.Status().Code() == ptrace.StatusCodeError {
			return false
		}

		sp := e.parseSpanSamplingPriority(span)
		if sp == doNotSampleSpan {
			// The OpenTelemetry mentions this as a "hint" we take a stronger
			// approach and do not sample the span since some may use it to
			// remove specific spans from traces.
			return true
		}

		// If one assumes random trace ids hashing may seems avoidable, however, traces can be coming from sources
		// with various different criteria to generate trace id and perhaps were already sampled without hashing.
		// Hashing here prevents bias due to such systems.
		tidBytes := span.TraceID().Bytes()
		sampled := sp == mustSampleSpan ||
			e.hash(tidBytes[:], e.hashSeed)&bitMaskHashBuckets < e.scaledSamplingRate
		return !sampled
	})
	return pdTraces
}

func (e randomEvaluator) parseSpanSamplingPriority(span ptrace.Span) samplingPriority {
	attribMap := span.Attributes()
	if attribMap.Len() <= 0 {
		return deferDecision
	}

	samplingPriorityAttrib, ok := attribMap.Get("sampling.priority")
	if !ok {
		return deferDecision
	}

	// By default defer the decision.
	decision := deferDecision

	// Try check for different types since there are various client libraries
	// using different conventions regarding "sampling.priority". Besides the
	// client libraries it is also possible that the type was lost in translation
	// between different formats.
	switch samplingPriorityAttrib.Type() {
	case pcommon.ValueTypeInt:
		value := samplingPriorityAttrib.IntVal()
		if value == 0 {
			decision = doNotSampleSpan
		} else if value > 0 {
			decision = mustSampleSpan
		}
	case pcommon.ValueTypeDouble:
		value := samplingPriorityAttrib.DoubleVal()
		if value == 0.0 {
			decision = doNotSampleSpan
		} else if value > 0.0 {
			decision = mustSampleSpan
		}
	case pcommon.ValueTypeString:
		attribVal := samplingPriorityAttrib.StringVal()
		if value, err := strconv.ParseFloat(attribVal, 64); err == nil {
			if value == 0.0 {
				decision = doNotSampleSpan
			} else if value > 0.0 {
				decision = mustSampleSpan
			}
		}
	}

	return decision
}

// hash is a murmur3 hash function, see http://en.wikipedia.org/wiki/MurmurHash
func (e randomEvaluator) hash(key []byte, seed uint32) (hash uint32) {
	const (
		c1 = 0xcc9e2d51
		c2 = 0x1b873593
		c3 = 0x85ebca6b
		c4 = 0xc2b2ae35
		r1 = 15
		r2 = 13
		m  = 5
		n  = 0xe6546b64
	)

	hash = seed
	iByte := 0
	for ; iByte+4 <= len(key); iByte += 4 {
		k := uint32(key[iByte]) | uint32(key[iByte+1])<<8 | uint32(key[iByte+2])<<16 | uint32(key[iByte+3])<<24
		k *= c1
		k = (k << r1) | (k >> (32 - r1))
		k *= c2
		hash ^= k
		hash = (hash << r2) | (hash >> (32 - r2))
		hash = hash*m + n
	}

	// TraceId and SpanId have lengths that are multiple of 4 so the code below is never expected to
	// be hit when sampling traces. However, it is preserved here to keep it as a correct murmur3 implementation.
	// This is enforced via tests.
	var remainingBytes uint32
	switch len(key) - iByte {
	case 3:
		remainingBytes += uint32(key[iByte+2]) << 16
		fallthrough
	case 2:
		remainingBytes += uint32(key[iByte+1]) << 8
		fallthrough
	case 1:
		remainingBytes += uint32(key[iByte])
		remainingBytes *= c1
		remainingBytes = (remainingBytes << r1) | (remainingBytes >> (32 - r1))
		remainingBytes *= c2
		hash ^= remainingBytes
	}

	hash ^= uint32(len(key))
	hash ^= hash >> 16
	hash *= c3
	hash ^= hash >> 13
	hash *= c4
	hash ^= hash >> 16

	return
}
