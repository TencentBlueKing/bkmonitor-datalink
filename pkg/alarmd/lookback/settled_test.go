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
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Values a store summed in another order are one value: these pairs are two
// reads of the same twenty-minute-old points in one deployment. A change a
// threshold could turn on is another value, however small next to that.
func TestValuesThatDifferOnlyInHowTheyWereSummedAreOneValue(t *testing.T) {
	for _, pair := range [][2]string{
		{"810051.6916666667", "810051.6916666663"},
		{"869455.4750000001", "869455.4749999999"},
		{"807011.3083333333", "807011.3083333328"},
		{"837278.1250000001", "837278.1249999998"},
		{"1", "1.0000000000000002"},
		{"-42.5", "-42.50000000000001"},
	} {
		if valueBits([]byte(pair[0])) != valueBits([]byte(pair[1])) {
			t.Errorf("%s and %s are two values, want one", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"810051.6916666667", "810051.70"},
		{"1", "1.00000001"},
		{"-42.5", "42.5"},
		{"0.1", "0.2"},
		{"+Inf", "-Inf"},
		{"+Inf", strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64)},
	} {
		if valueBits([]byte(pair[0])) == valueBits([]byte(pair[1])) {
			t.Errorf("%s and %s are one value, want two", pair[0], pair[1])
		}
	}
	if valueBits([]byte("-0")) != valueBits([]byte("0")) || valueBits([]byte("NaN")) != valueBits([]byte("nan")) {
		t.Error("-0 and 0, or two NaNs, are two values")
	}
}

// The same values summed in three orders - one after another, in 64 shards
// added up at the end, and backwards - at close to the most a query sums,
// 2^25, come back three different doubles and one value. The terms are made
// from their index, so no order needs them all held at once.
func TestASumNearTheLargestReadIsOneValueInAnyOrder(t *testing.T) {
	const n = 1<<25 - 1
	term := func(i uint64) float64 {
		// splitmix64 of the index, to a value in [1, 1001).
		z := i + 0x9e3779b97f4a7c15
		z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
		z = (z ^ z>>27) * 0x94d049bb133111eb
		z ^= z >> 31
		return 1 + float64(z>>11)/(1<<53)*1000
	}
	forward, backward := 0.0, 0.0
	var shards [64]float64
	for i := uint64(0); i < n; i++ {
		value := term(i)
		forward += value
		shards[i%64] += value
		backward += term(n - 1 - i)
	}
	sharded := 0.0
	for _, shard := range shards {
		sharded += shard
	}
	sums := []float64{forward, sharded, backward}
	if forward == sharded && sharded == backward {
		t.Fatalf("the three orders summed to one double %v; the case needs their noise", forward)
	}
	for _, sum := range sums[1:] {
		left, right := strconv.FormatFloat(forward, 'g', -1, 64), strconv.FormatFloat(sum, 'g', -1, 64)
		if valueBits([]byte(left)) != valueBits([]byte(right)) {
			t.Errorf("%s and %s, one sum in two orders, are two values", left, right)
		}
	}
}

// A Query Group whose every read of a window comes back different only in
// how its store summed it is complete: no rung changed, and it is not read
// early however many samples say so.
func TestAGroupWhoseReadsDifferOnlyInHowTheyWereSummedIsComplete(t *testing.T) {
	f := newFixture(t)
	for range 3 {
		f.classSample(0, []*execution.Dataset{point(f.clock.now().Unix(), "810051.6916666667")},
			func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "810051.6916666663")} })
	}
	source := f.engine.Stats().Sources[sourceLog]
	if source.Classes[ClassComplete] != 3 || source.Classes[ClassWindowReadEarly] != 0 || source.ChangedWindows[RungNames[0]] != 0 ||
		len(f.engine.ReadEarly()) != 0 {
		t.Fatalf("classes %v changed %v read early %+v, want three complete samples", source.Classes, source.ChangedWindows,
			f.engine.ReadEarly())
	}
}

// A sample is classed by what its data settled to. A value that changed and
// came back was judged as it stands, and data that came to an empty first
// read and went again was not there to judge: both are complete, where a
// class kept from the first rung that differed called them read early.
func TestAChangeThatCameBackIsNotAWindowReadEarly(t *testing.T) {
	f := newFixture(t)
	f.classSampleByRung(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, func(slot int64, rung int) []*execution.Dataset {
		if rung == 0 {
			return []*execution.Dataset{point(slot, "3")}
		}
		return []*execution.Dataset{point(slot, "1")}
	})
	f.classSampleByRung(0, nil, func(slot int64, rung int) []*execution.Dataset {
		if rung == 0 {
			return []*execution.Dataset{point(slot, "1")}
		}
		return nil
	})
	source := f.engine.Stats().Sources[sourceLog]
	if source.Classes[ClassComplete] != 2 || source.Classes[ClassWindowReadEarly] != 0 || source.Classes[ClassSeriesLate] != 0 ||
		source.ChangedWindows[RungNames[1]] != 2 {
		t.Fatalf("classes %v changed %v, want both complete after their second rung changed back", source.Classes, source.ChangedWindows)
	}
	if state := f.group("qg"); state.readEarly != nil || state.seriesLate != nil {
		t.Fatalf("a change that came back marked its group: %+v %+v", state.readEarly, state.seriesLate)
	}
	// Changed and held, the same sample is read early: two later reads
	// agree, and both differ from the first.
	f.classSampleByRung(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, func(slot int64, rung int) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "3")}
	})
	if source := f.engine.Stats().Sources[sourceLog]; source.Classes[ClassWindowReadEarly] != 1 {
		t.Fatalf("classes %v, want a change that held to be read early", source.Classes)
	}
}

// A sample still changing at the deepest rung has no later read to say its
// data settled: unclassified as unsettled, which neither starts a run of
// window_read_early nor ends one.
func TestASampleStillChangingAtTheDeepestRungIsUnsettled(t *testing.T) {
	f := newFixture(t)
	f.classSampleByRung(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, func(slot int64, rung int) []*execution.Dataset {
		return []*execution.Dataset{point(slot, strconv.Itoa(rung+2))}
	})
	stats := f.engine.Stats()
	source := stats.Sources[sourceLog]
	if source.Classes[ClassUnclassified] != 1 || source.Unclassified[UnclassifiedUnsettled] != 1 ||
		source.Unclassified[UnclassifiedMemoryRefused] != 0 || source.Classes[ClassWindowReadEarly] != 0 ||
		source.ChangedWindows[RungNames[len(RungNames)-1]] != 1 {
		t.Fatalf("classes %v unclassified %v changed %v, want one unsettled sample changed at every rung", source.Classes,
			source.Unclassified, source.ChangedWindows)
	}
	if state := f.group("qg"); state.readEarly != nil {
		t.Fatalf("an unsettled sample started a run: %+v", state.readEarly)
	}
}

// A group whose window is reported read early does not rest longer from one
// sample to the next, whatever the sample read: its rest stays at its
// deepest rung's, so the sample that can withdraw the report comes that soon
// after and not up to restCap later. Withdrawn, its rest grows again.
func TestAGroupReadEarlyRestsNoLongerThanItsDeepestRung(t *testing.T) {
	f := newFixture(t)
	revised := func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "3")} }
	for range 3 {
		// Read to its last rung and measured there, the moment it finished,
		// before the clock is moved on to the next sample.
		slot := f.clock.now().Unix()
		q := query("qg", slot, minute, sourceLog)
		q.Spec.PlanFacts.QueryDelaySeconds = 60
		f.capture(q, point(slot, "1"))
		readAt := f.clock.now()
		for rung, pending := f.rung("qg"); pending; rung, pending = f.rung("qg") {
			f.recheck(sourceLog, readAt, rung, minute, full(revised(slot)...), RecheckCompared)
		}
		state := f.group("qg")
		floor := RungSteps[state.depth-1]
		limit := time.Duration(floor * float64(state.step) * 1.25)
		if state.rest != floor || state.nextAt.Sub(f.clock.now()) > limit {
			t.Fatalf("read early: rest %v steps, next sample in %v; want %v steps, at most %v", state.rest,
				state.nextAt.Sub(f.clock.now()), floor, limit)
		}
		f.rest("qg")
	}
	if len(f.engine.ReadEarly()) != 1 {
		t.Fatalf("read early %+v, want the group reported", f.engine.ReadEarly())
	}
	// One complete sample leaves it reported, and its rest where it was.
	f.classSample(60, []*execution.Dataset{point(f.clock.now().Unix(), "3")}, revised)
	if state := f.group("qg"); state.rest != RungSteps[state.depth-1] || len(f.engine.ReadEarly()) != 1 {
		t.Fatalf("one complete: rest %v steps, read early %+v; want the rest held and the report kept", state.rest, f.engine.ReadEarly())
	}
	f.classSample(60, []*execution.Dataset{point(f.clock.now().Unix(), "3")}, revised)
	if state := f.group("qg"); state.rest <= RungSteps[state.depth-1] || len(f.engine.ReadEarly()) != 0 {
		t.Fatalf("two complete: rest %v steps, read early %+v; want the rest to grow and the report gone", state.rest, f.engine.ReadEarly())
	}
}

// A first sample read early after complete ones, at a depth the group has
// already learned and with a rest grown long, brings the rest back to the
// deepest rung's: the sample that can confirm the report comes that soon
// after, not up to restCap later.
func TestAFirstEarlySampleAfterCompleteOnesClampsTheRest(t *testing.T) {
	f := newFixture(t)
	revised := func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "3")} }
	f.classSample(60, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, revised)
	depth := f.group("qg").depth
	for range 3 {
		f.classSample(60, []*execution.Dataset{point(f.clock.now().Unix(), "3")}, revised)
	}
	state := f.group("qg")
	if floor := RungSteps[state.depth-1]; state.depth != depth || state.rest <= floor {
		t.Fatalf("setup: depth %d (was %d), rest %v steps, floor %v", state.depth, depth, state.rest, floor)
	}
	f.classSample(60, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, revised)
	if state = f.group("qg"); state.rest != RungSteps[state.depth-1] {
		t.Fatalf("one early sample after complete ones: rest %v steps, want the floor %v", state.rest, RungSteps[state.depth-1])
	}
}
