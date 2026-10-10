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
	"sort"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A read is kept as a summary per bucket (a record's source time): how many
// points the bucket had, and two sums over its points - of each point's
// series, and of each point's series, time and value - every term hashed to
// 64 bits and the sums taken modulo 2^64. Sums do not depend on the order the
// points arrive in, so the same data read in another series or page order
// sums to the same bits, and a bucket is three words whatever the number of
// series. That is what lets every owned Query Group keep a sample.
type bucketSummary struct {
	points uint64
	series uint64
	values uint64
}

// readSummary is one read's buckets by source time.
type readSummary map[int64]bucketSummary

// summaryEntryBytes is what one bucket is charged: its key, its three words
// and the map's share of an entry.
const summaryEntryBytes = 48

func (summary readSummary) bytes() int { return len(summary) * summaryEntryBytes }

// maxBucketsPerSample guards against a window or step read wrongly. A day
// at one-second steps is 86400 buckets; no Query Group's window reaches
// 2^17 buckets, so a read that does is a defect to fix, counted as a fault
// and not kept, never a bound that normal running meets.
const maxBucketsPerSample = 1 << 17

// seriesSummary is one series over the kept tail, summed the way a bucket is:
// how many points it had there and the sum of their point hashes. Two reads of
// the series that agree on it had the same points with the same values.
// active is how many of its points had a value other than zero or none: a
// series every read has at zero or without a value is quiet there, which a
// source that fills its empty buckets with zero makes of every series with
// nothing in them. points fits in 32 bits: a sample has at most
// maxBucketsPerSample buckets.
type seriesSummary struct {
	points uint32
	active uint32
	values uint64
}

// seriesEntryBytes is what one series is charged: its key, its two words
// and the map's share of an entry, as the heap holds them after a
// collection - measured at 44 to 53 bytes, charged a little above.
const seriesEntryBytes = 56

// seriesAdmitFirst is how many series a table asks the process's memory
// line for first; every later ask doubles what it holds. What a table has
// been admitted is then never more than twice what it uses - the line counts
// an admission until the next collection, so room asked for and not used
// would crowd out what does use it - and a table of n series asks about
// log2(n/64) times, off the path every point takes.
const seriesAdmitFirst = 64

// summarizer builds a readSummary from delivered series, and, from
// seriesFrom on, a summary per series: which series a read had, and what.
//
// The series table has no bound of its own. It grows as the process's
// memory line admits it, doubling; once the line refuses, the read stops
// summing series - the table is dropped - and keeps its buckets, so the
// sample goes on and only what the series would have told is not known.
type summarizer struct {
	valueField string
	buckets    readSummary
	series     map[uint64]seriesSummary
	seriesFrom int64
	admit      func(bytes uint64) bool
	granted    int
	buffer     []byte
	faulted    bool
}

func newSummarizer(valueField string) *summarizer {
	return &summarizer{valueField: valueField, buckets: readSummary{}}
}

// trackSeries sums every series too, over its records from from on, as far
// as admit lets the table grow; a nil admit lets it grow as far as it goes.
func (summarizer *summarizer) trackSeries(from int64, admit func(bytes uint64) bool) *summarizer {
	summarizer.series, summarizer.seriesFrom, summarizer.admit = map[uint64]seriesSummary{}, from, admit
	return summarizer
}

// grow asks for room for as many series again as the table was admitted,
// seriesAdmitFirst the first time, and stops the series sums for good when
// it is refused.
func (summarizer *summarizer) grow() bool {
	step := max(summarizer.granted, seriesAdmitFirst)
	if summarizer.admit != nil && !summarizer.admit(uint64(step)*seriesEntryBytes) {
		summarizer.series = nil
		return false
	}
	summarizer.granted += step
	return true
}

// add sums one delivered series: every record's bucket, and the series.
func (summarizer *summarizer) add(dataset *execution.Dataset) {
	if summarizer.faulted || dataset == nil || dataset.Len() == 0 {
		return
	}
	first, _ := dataset.Record(0)
	series := hashString(first.DimensionIdentityDigest())
	seriesTerm := mix(series)
	for index := 0; index < dataset.Len(); index++ {
		record, _ := dataset.Record(index)
		summarizer.addRecord(record, series, seriesTerm)
		if summarizer.faulted {
			return
		}
	}
}

func (summarizer *summarizer) addRecord(record execution.RecordView, series, seriesTerm uint64) {
	at := record.SourceTime()
	bucket, known := summarizer.buckets[at]
	if !known && len(summarizer.buckets) >= maxBucketsPerSample {
		summarizer.fault()
		return
	}
	summarizer.buffer, _ = record.AppendValue(summarizer.buffer[:0], summarizer.valueField)
	bits := valueBits(summarizer.buffer)
	point := pointHash(series, at, bits)
	bucket.points++
	bucket.series += seriesTerm
	bucket.values += point
	summarizer.buckets[at] = bucket
	if summarizer.series == nil || at < summarizer.seriesFrom {
		return
	}
	sum, known := summarizer.series[series]
	if !known && len(summarizer.series) >= summarizer.granted && !summarizer.grow() {
		return
	}
	sum.points++
	if !quietValue(summarizer.buffer, bits) {
		sum.active++
	}
	sum.values += point
	summarizer.series[series] = sum
}

// fault drops a read past every window's buckets.
func (summarizer *summarizer) fault() {
	summarizer.faulted = true
	summarizer.buckets, summarizer.series = nil, nil
}

// quietValue is whether a point's value is none -- absent or null -- or
// zero: what a source that fills empty buckets gives a series with nothing
// in them.
func quietValue(text []byte, bits uint64) bool {
	return len(text) == 0 || string(text) == "null" || bits == 0
}

// seriesChange is how a later read's series stand against the first read's:
// how many of the first read's series it has with other points or values, or
// has lost, and how many it has that the first read did not. existingSteady
// is how many of the first read's series it has as they were and with a
// value there: series the first read already had whole. existingArrived is
// how many of the changed ones it has with a value there: data that came
// after the first read, as against a series that went. A series quiet in
// both reads -- zero or no value at every point -- is neither steady nor
// changed: it says nothing about whether the window was read early.
type seriesChange struct {
	existingChanged int
	existingSteady  int
	existingArrived int
	added           int
}

// compareSeries compares a later read's series with the first read's.
func compareSeries(first, later map[uint64]seriesSummary) seriesChange {
	var change seriesChange
	for series, after := range later {
		before, present := first[series]
		switch {
		case !present:
			change.added++
		case before.active == 0 && after.active == 0:
			// Quiet in both reads: its points may have come or gone, but
			// with no value either time it says nothing of the window.
		case before != after:
			change.existingChanged++
			if after.active > 0 {
				change.existingArrived++
			}
		case after.active > 0:
			change.existingSteady++
		}
	}
	for series, before := range first {
		// A series that went counts, unless it was quiet: it had nothing
		// to lose.
		if _, present := later[series]; !present && before.active > 0 {
			change.existingChanged++
		}
	}
	return change
}

// changedBuckets is the buckets of later that differ from earlier, oldest
// first, at most limit of them.
func changedBuckets(earlier, later readSummary, limit int) []int64 {
	var changed []int64
	for at, after := range later {
		if earlier[at] != after {
			changed = append(changed, at)
		}
	}
	for at := range earlier {
		if _, present := later[at]; !present {
			changed = append(changed, at)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })
	if len(changed) > limit {
		changed = changed[:limit]
	}
	return changed
}

// valueFractionBits is how many of a number's 52 fraction bits it is compared
// by, set from the bound on a sum's rounding error. A store sums a query's
// series in no fixed order, and a sum of n doubles, in any order, is within
// (n-1)*2^-53*sum|x| of the exact sum. One query sums fewer than 2^25
// values - the largest single read measured, on the largest deployment, is
// about 1.64 million points, and 2^25 is about 33.5 million - so each read
// is within 2^-28*sum|x| of the exact sum, and 2^-28 is the unit of the 28th
// fraction bit relative to the value. For terms of one sign, which counts
// and sums of rates are, sum|x| is the value itself, and every read of the
// same data rounds to the same 28 bits unless the exact sum lies within that
// error of a rounding boundary. The error met in practice is far smaller:
// three reads of the same twenty-minute-old window of a sum by one
// dimension over many series differed at every point, by at most about
// 1.4e-15 of the value, and compared bit for bit every rung of such a group
// found a value revised after it was judged and reported the group read
// early. At that noise a boundary falls between two reads about once in a
// million values. A sum whose terms cancel, whose sum|x| is far larger than
// the value, can still read as changed; a revision smaller than one part in
// 2^28 is not one a threshold turns on.
const valueFractionBits = 28

// valueBits is a value as the bits it is compared by: a number's IEEE bits
// rounded to valueFractionBits, so the same number rendered two ways, or
// summed in another order, is one value, with -0 read as 0, every NaN as
// one and an infinity as itself; anything else by a hash of its text.
func valueBits(text []byte) uint64 {
	if number, err := strconv.ParseFloat(string(text), 64); err == nil {
		switch {
		case number == 0:
			return 0
		case math.IsNaN(number):
			return math.Float64bits(math.NaN())
		case math.IsInf(number, 0):
			return math.Float64bits(number)
		default:
			// Half of the dropped part added before it is cleared rounds to
			// the nearest; a carry out of the fraction moves the exponent
			// up, which is the next representable value in order. Only the
			// largest finite numbers could carry into infinity, and they are
			// kept as they are.
			const dropped = 52 - valueFractionBits
			bits := math.Float64bits(number)
			rounded := (bits + 1<<(dropped-1)) &^ (1<<dropped - 1)
			if math.IsInf(math.Float64frombits(rounded), 0) {
				return bits
			}
			return rounded
		}
	}
	return hashBytes(text) ^ 0xa0761d6478bd642f
}

// Changes between two reads of one bucket, closed.
const (
	ChangePointsAdded   = "points_added"
	ChangePointsRemoved = "points_removed"
	ChangeSeriesChanged = "series_changed"
	ChangeValuesChanged = "values_changed"
)

// Changes is every change class.
var Changes = []string{ChangePointsAdded, ChangePointsRemoved, ChangeSeriesChanged, ChangeValuesChanged}

// compareSummaries counts the buckets of later that differ from earlier, by
// class: more points than before, fewer, as many from another set of series,
// or the same series with other values.
func compareSummaries(earlier, later readSummary) map[string]int {
	changes := map[string]int{}
	classify := func(before, after bucketSummary) {
		switch {
		case before == after:
		case after.points > before.points:
			changes[ChangePointsAdded]++
		case after.points < before.points:
			changes[ChangePointsRemoved]++
		case after.series != before.series:
			changes[ChangeSeriesChanged]++
		default:
			changes[ChangeValuesChanged]++
		}
	}
	for at, after := range later {
		classify(earlier[at], after)
	}
	for at, before := range earlier {
		if _, present := later[at]; !present {
			classify(before, bucketSummary{})
		}
	}
	return changes
}

// hashBytes and hashString are 64-bit FNV-1a, spelled out so hashing a
// point allocates nothing.
func hashBytes(data []byte) uint64 {
	hash := uint64(14695981039346656037)
	for _, b := range data {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return hash
}

func hashString(text string) uint64 {
	hash := uint64(14695981039346656037)
	for index := 0; index < len(text); index++ {
		hash ^= uint64(text[index])
		hash *= 1099511628211
	}
	return hash
}

// mix is the splitmix64 finalizer: every input bit reaches every output bit,
// so sums of mixed terms do not cancel the way sums of raw hashes can.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func pointHash(series uint64, at int64, value uint64) uint64 {
	return mix(series ^ mix(uint64(at)+0x9e3779b97f4a7c15) ^ mix(value+0x632be59bd9b4e019))
}
