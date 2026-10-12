// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Data that arrives after a first read and no later than the deepest rung
// lands in the window's last tailSteps steps: a bucket older than that was
// complete before the first read. So a sample keeps, and its rechecks read
// and compare, only that tail - the whole of a shorter window - however deep
// its Query Group reads: the cost follows the deepest rung, not the window's
// length. A group reading too few rungs sees its data arrive in the tail at
// the rungs it does read and deepens, as long as the window reaches back as
// far as the data is late; a shorter window can be empty at every rung it
// reads, and the deep recheck finds that one.
var tailSteps = int64(math.Ceil(RungSteps[len(RungSteps)-1])) + 1

// tailFrom is the first bucket of a window's last steps, on the window's
// own grid and never before the window.
func tailFrom(window execution.QueryWindow, step time.Duration, steps int64) int64 {
	seconds := int64(step / time.Second)
	if seconds <= 0 {
		return window.Start
	}
	from := window.End - steps*seconds
	if from <= window.Start {
		return window.Start
	}
	return window.Start + (from-window.Start)/seconds*seconds
}

// trimSummary is the buckets of a summary from from on.
func trimSummary(summary readSummary, from int64) readSummary {
	trimmed := make(readSummary, len(summary))
	for at, bucket := range summary {
		if at >= from {
			trimmed[at] = bucket
		}
	}
	return trimmed
}

// tailSpec is the frozen physical query reading from start rather than from
// its window's start: the same request, so the same series identities and
// record ids, over the tail of each of its ranges, under its own digest. A
// start inside no range, or a spec whose digest cannot be derived again,
// reads the whole window as before.
func tailSpec(spec execution.PhysicalQuerySpec, start int64) execution.PhysicalQuerySpec {
	if start <= spec.LogicalWindow.Start || start >= spec.LogicalWindow.End {
		return spec
	}
	tail := spec
	tail.LogicalWindow.Start = max(tail.LogicalWindow.Start, start)
	tail.ProviderRange.Start = max(tail.ProviderRange.Start, start)
	tail.AcceptedRange.Start = max(tail.AcceptedRange.Start, start)
	digest, err := execution.DerivePhysicalQueryDigest(tail)
	if err != nil {
		return spec
	}
	tail.Digest = digest
	return tail
}

// queryLookback is how far before a bucket a query reads to compute it: the
// longest time aggregation window and function window of any clause, and its
// offset; for PromQL the longest range or subquery range and offset. A tail
// read starts that much earlier, so its first compared bucket is computed
// from the same data the first read computed it from. false when a duration
// cannot be read.
func queryLookback(facts execution.QueryPlanFacts) (time.Duration, bool) {
	if facts.PromQL != nil {
		return promQLLookback(facts.PromQL.Expression)
	}
	longest := time.Duration(0)
	for _, clause := range facts.QueryList {
		window, ok := parseDuration(clause.TimeAggregation.Window)
		if !ok {
			return 0, false
		}
		functions := time.Duration(0)
		for _, function := range clause.Functions {
			length, ok := parseDuration(function.Window)
			if !ok {
				return 0, false
			}
			functions = max(functions, length)
		}
		offset, ok := parseDuration(clause.Offset)
		if !ok {
			return 0, false
		}
		// A backward offset reads further back; a forward one - the shift a
		// Plan detected more often than it aggregates labels its buckets
		// with - reads later data for the same bucket, so less far back.
		if clause.OffsetForward == "true" {
			offset = -offset
		}
		longest = max(longest, window+functions+offset)
	}
	return longest, true
}

var (
	promQLRange  = regexp.MustCompile(`\[([^\]:]+)(?::[^\]]*)?\]`)
	promQLOffset = regexp.MustCompile(`\boffset\s+(-?[0-9A-Za-z]+)`)
)

func promQLLookback(expression string) (time.Duration, bool) {
	ranges, offsets := time.Duration(0), time.Duration(0)
	for _, match := range promQLRange.FindAllStringSubmatch(expression, -1) {
		length, ok := parseDuration(match[1])
		if !ok {
			return 0, false
		}
		ranges = max(ranges, length)
	}
	for _, match := range promQLOffset.FindAllStringSubmatch(expression, -1) {
		text := match[1]
		if len(text) > 0 && text[0] == '-' {
			text = text[1:]
		}
		length, ok := parseDuration(text)
		if !ok {
			return 0, false
		}
		offsets = max(offsets, length)
	}
	return ranges + offsets, true
}

var durationUnits = map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour,
	"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "y": 365 * 24 * time.Hour}

var durationPart = regexp.MustCompile(`^([0-9]+)(ms|s|m|h|d|w|y)`)

// parseDuration reads a query duration: whole numbers with a unit, one
// after another (1h30m), as PromQL and the query service write them. Empty
// is none.
func parseDuration(text string) (time.Duration, bool) {
	if text == "" {
		return 0, true
	}
	total := time.Duration(0)
	for text != "" {
		match := durationPart.FindStringSubmatch(text)
		if match == nil {
			return 0, false
		}
		number, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return 0, false
		}
		total += time.Duration(number) * durationUnits[match[2]]
		text = text[len(match[0]):]
	}
	return total, true
}
