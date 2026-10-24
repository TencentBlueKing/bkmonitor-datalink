// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"math"
	"math/bits"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ResizeQueryLimits puts a new query budget in force: the process and
// recovery permits and the two waiter queues. The queues follow the work this
// process owns, so they change while the process runs; the permits are the
// memory's share and a resize normally carries them unchanged.
//
// Raising it grants the queries already waiting at once. Lowering it takes
// nothing back: permits already held run to their release, and none is
// granted until the held fall under the new budget. A shape the coordinator
// cannot run under is refused whole and the one in force stays, as is a
// resize that would change anything but the budget - the replay limits are
// not derived from the work and do not move with it.
func (coordinator *FlightCoordinator) ResizeQueryLimits(limits RecoveryLimits) error {
	if coordinator == nil || !coordinator.recoveryEnabled || limits.Validate() != nil {
		return ErrRecoveryLimitsInvalid
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	budgetOnly := coordinator.limits
	budgetOnly.ProcessQueryPermits, budgetOnly.RecoveryQueryPermits = limits.ProcessQueryPermits, limits.RecoveryQueryPermits
	budgetOnly.ReadyQueueCapacity, budgetOnly.RecoveryQueueCapacity = limits.ReadyQueueCapacity, limits.RecoveryQueueCapacity
	if budgetOnly != limits {
		return ErrRecoveryLimitsInvalid
	}
	coordinator.limits = limits
	coordinator.dispatchQueryPermitsLocked()
	coordinator.dispatchRecoveryChannelsLocked()
	return nil
}

// HoldReference is how long a normal query holds its permit when the query
// service is not under pressure: the lowest hourly mean of the normal holds
// over the last day, the current hour included. Zero before any hold. It
// sizes nothing; the capacity reports it beside the budget.
//
// The mean and not the median, because how many permits a rate keeps busy is
// the rate times the mean hold (Little's law): a service that answers most
// queries in 150 ms and one in five in 3 s keeps 0.72 s of permit per query,
// and a median would read a fifth of that.
func (coordinator *FlightCoordinator) HoldReference() time.Duration {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.holds.MeanReference(coordinator.now())
}

// holdHourBuckets is how finely an hour's holds are kept: bucket i holds
// those from 2^(i/2) ms up to the next bucket's start, so a median read off
// it is within a factor of the square root of two, and the last bucket holds
// everything from about 18 minutes up.
const holdHourBuckets = 42

// holdHour is one hour's durations, as a histogram and their exact sum.
type holdHour struct {
	start   int64 // unix hour; zero for an hour never written
	count   uint64
	sum     time.Duration
	buckets [holdHourBuckets]uint32
}

// HourlyDurations is the last day's durations of one kind - permit holds,
// executions - one histogram and sum per hour in a ring, read as the lowest
// hourly median (Reference) or mean (MeanReference). Fixed size: recording
// allocates nothing. Not safe for concurrent use; its owner holds a lock
// around it.
type HourlyDurations struct {
	hours [24]holdHour
}

func holdBucket(d time.Duration) int {
	millis := uint64(max(d.Milliseconds(), 1))
	exponent := bits.Len64(millis) - 1 // 2^exponent <= millis
	index := 2 * exponent
	// The upper half of the octave: millis at or past 2^exponent * sqrt(2),
	// compared squared to stay in integers.
	if millis*millis >= 2<<(2*uint(exponent)) {
		index++
	}
	return min(index, holdHourBuckets-1)
}

// holdBucketMiddle is a bucket's midpoint, what a median in it reads as.
func holdBucketMiddle(index int) time.Duration {
	low := 1 << (index / 2)
	if index%2 == 1 {
		low = low * 1414 / 1000
	}
	high := 1 << ((index + 1) / 2)
	if (index+1)%2 == 1 {
		high = high * 1414 / 1000
	}
	return time.Duration(low+high) * time.Millisecond / 2
}

// holdBucketHigh is a bucket's upper edge, what a tail read as a reserve
// rounds up to: reserving the middle would leave the slower half of the
// bucket's executions short.
func holdBucketHigh(index int) time.Duration {
	high := 1 << ((index + 1) / 2)
	if (index+1)%2 == 1 {
		high = high * 1414 / 1000
	}
	return time.Duration(high) * time.Millisecond
}

// Record keeps one duration, in the hour at falls in.
func (history *HourlyDurations) Record(at time.Time, hold time.Duration) {
	hour := at.Unix() / 3600
	slot := &history.hours[hour%int64(len(history.hours))]
	if slot.start != hour {
		*slot = holdHour{start: hour}
	}
	slot.count++
	slot.sum += max(hold, 0)
	slot.buckets[holdBucket(hold)]++
}

// Count is how many durations the day before at holds, the current hour
// included.
func (history *HourlyDurations) Count(at time.Time) uint64 {
	now := at.Unix() / 3600
	var total uint64
	for index := range history.hours {
		hour := &history.hours[index]
		if hour.count == 0 || hour.start <= now-int64(len(history.hours)) || hour.start > now {
			continue
		}
		total += hour.count
	}
	return total
}

// Quantile is the q-quantile (0 < q <= 1) of every duration of the day
// before at, the current hour included, read at its bucket's upper edge;
// zero before any duration. Unlike Reference it pools the hours instead of
// taking the quietest one: it is read where the slow tail is the point - a
// reserve sized from the quietest hour would be short in exactly the hour
// that needs it.
func (history *HourlyDurations) Quantile(at time.Time, q float64) time.Duration {
	if q <= 0 || q > 1 {
		return 0
	}
	now := at.Unix() / 3600
	var pooled [holdHourBuckets]uint64
	var total uint64
	for index := range history.hours {
		hour := &history.hours[index]
		if hour.count == 0 || hour.start <= now-int64(len(history.hours)) || hour.start > now {
			continue
		}
		for bucket, count := range hour.buckets {
			pooled[bucket] += uint64(count)
		}
		total += hour.count
	}
	if total == 0 {
		return 0
	}
	// The rank is rounded up, so with fewer durations than 1/(1-q) the
	// quantile is the slowest one seen rather than one below it.
	rank := uint64(math.Ceil(q * float64(total)))
	var seen uint64
	for bucket, count := range pooled {
		seen += count
		if seen >= rank {
			return holdBucketHigh(bucket)
		}
	}
	return holdBucketHigh(holdHourBuckets - 1)
}

// Reference is the lowest median of the hours of the day before at, the
// current one included; zero before any duration.
func (history *HourlyDurations) Reference(at time.Time) time.Duration {
	return history.lowest(at, (*holdHour).median)
}

// MeanReference is the lowest mean of the hours of the day before at, the
// current one included; zero before any duration.
func (history *HourlyDurations) MeanReference(at time.Time) time.Duration {
	return history.lowest(at, (*holdHour).mean)
}

// lowest reads the lowest value among the hours that hold at least a tenth
// as many durations as the busiest hour of the day. An hour that has only
// begun - a few fast holds in its first minute - says little about the
// service, and as the lowest it would pull the budget down until it filled.
// Relative to the busiest hour, so a quiet deployment's hours all count and
// the first hour after a start counts on its own.
func (history *HourlyDurations) lowest(at time.Time, read func(*holdHour) time.Duration) time.Duration {
	now := at.Unix() / 3600
	live := func(hour *holdHour) bool {
		return hour.count > 0 && hour.start > now-int64(len(history.hours)) && hour.start <= now
	}
	var busiest uint64
	for index := range history.hours {
		if hour := &history.hours[index]; live(hour) {
			busiest = max(busiest, hour.count)
		}
	}
	var lowest time.Duration
	for index := range history.hours {
		hour := &history.hours[index]
		if !live(hour) || hour.count*holdHourQuorumDivisor < busiest {
			continue
		}
		if value := read(hour); lowest == 0 || value < lowest {
			lowest = value
		}
	}
	return lowest
}

// holdHourQuorumDivisor: an hour counts toward the lowest once it holds a
// tenth of the busiest hour's durations.
const holdHourQuorumDivisor = 10

// mean is never below a millisecond, the histogram's own floor: a zero
// would read as no hold at all, and the shape would size from the
// cold-start guess instead.
func (hour *holdHour) mean() time.Duration {
	return max(hour.sum/time.Duration(hour.count), time.Millisecond)
}

func (hour *holdHour) median() time.Duration {
	var seen uint64
	for index, count := range hour.buckets {
		seen += uint64(count)
		if 2*seen >= hour.count {
			return holdBucketMiddle(index)
		}
	}
	return holdBucketMiddle(holdHourBuckets - 1)
}

// PostQueryTailQuantile is the post-query tail's slow end that the downstream
// execution reserve is sized from, the 99th percentile: the reserve exists so
// a Slot that runs long after its query - a slow state read, a Kafka retry -
// still completes, and sizing it from a typical Slot would leave one in a
// hundred short.
const PostQueryTailQuantile = 0.99

// PostQueryTailMinSamples is how many tails the day must hold before
// PostQueryTail reads one: a 99th percentile exists only among a hundred
// (1/(1-PostQueryTailQuantile)). Below that the quantile is the slowest tail
// seen, and after a restart a handful of fast Slots would take the reserve
// from the frozen value to a fraction of a second, leaving the first slow
// state write short. Not a picked number: it follows from the percentile.
const PostQueryTailMinSamples = 100

// SetLiveReserve gives the coordinator the runtime's live reserve, which keeps
// each Query Group's own tails beside the pooled ones and counts the Slots
// that overran.
func (coordinator *FlightCoordinator) SetLiveReserve(live *execution.LiveReserve) {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.live = live
}

// LiveReserve is the measured post-query tail SetLiveReserve gave the
// coordinator; nil before one is set.
func (coordinator *FlightCoordinator) LiveReserve() *execution.LiveReserve {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.live
}

// RecordPostQueryTail keeps one Slot's post-query tail, pooled and as
// queryGroup's own.
func (coordinator *FlightCoordinator) RecordPostQueryTail(at time.Time, queryGroup execution.QueryGroupIdentity, tail time.Duration) {
	if coordinator == nil || tail <= 0 {
		return
	}
	coordinator.mu.Lock()
	coordinator.postQueryTails.Record(at, tail)
	live := coordinator.live
	coordinator.mu.Unlock()
	live.RecordGroupTail(queryGroup, at, tail)
}

// CountOverrun counts a Slot that returned after its completion deadline.
func (coordinator *FlightCoordinator) CountOverrun() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	live := coordinator.live
	coordinator.mu.Unlock()
	live.CountOverrun()
}

// PostQueryTail is the last day's post-query tail at PostQueryTailQuantile,
// pooled over its hours; zero until the day holds PostQueryTailMinSamples.
func (coordinator *FlightCoordinator) PostQueryTail() time.Duration {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	now := coordinator.now()
	if coordinator.postQueryTails.Count(now) < PostQueryTailMinSamples {
		return 0
	}
	return coordinator.postQueryTails.Quantile(now, PostQueryTailQuantile)
}

// recordHoldLocked keeps a normal query's hold for HoldReference.
func (coordinator *FlightCoordinator) recordHoldLocked(operation execution.Operation, hold time.Duration) {
	if operation == execution.OperationNormal {
		coordinator.holds.Record(coordinator.now(), hold)
	}
}

// AcquisitionCounts is how many query permits this process has been asked
// for since it started: normal and recovery. The scheduler shape reads their
// rates.
func (coordinator *FlightCoordinator) AcquisitionCounts() (normal, recovery uint64) {
	if coordinator == nil {
		return 0, 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.permitAcquires - coordinator.recoveryAcquires, coordinator.recoveryAcquires
}

// SecondPeak counts events by the second they happen in, over the last
// minute, and reads the busiest second. Fixed size: counting allocates
// nothing. Not safe for concurrent use; its owner holds a lock around it.
type SecondPeak struct {
	seconds [64]int64
	counts  [64]uint32
}

// Add counts one event at at.
func (peak *SecondPeak) Add(at time.Time) {
	second := at.Unix()
	slot := second & 63
	if peak.seconds[slot] != second {
		peak.seconds[slot], peak.counts[slot] = second, 0
	}
	peak.counts[slot]++
}

// Peak is the most events in one second of the minute before at.
func (peak *SecondPeak) Peak(at time.Time) int {
	now := at.Unix()
	most := uint32(0)
	for slot, second := range peak.seconds {
		if second > now-60 && second <= now {
			most = max(most, peak.counts[slot])
		}
	}
	return int(most)
}

// SecondMax keeps the highest value seen in each second of the last minute
// and reads the highest of them. Fixed size: observing allocates nothing. Not
// safe for concurrent use; its owner holds a lock around it.
type SecondMax struct {
	seconds [64]int64
	values  [64]int
}

// Observe keeps value for the second at falls in, if it is that second's
// highest so far.
func (peak *SecondMax) Observe(at time.Time, value int) {
	second := at.Unix()
	slot := second & 63
	if peak.seconds[slot] != second {
		peak.seconds[slot], peak.values[slot] = second, 0
	}
	peak.values[slot] = max(peak.values[slot], value)
}

// Max is the highest value observed in the minute before at.
func (peak *SecondMax) Max(at time.Time) int {
	now := at.Unix()
	most := 0
	for slot, second := range peak.seconds {
		if second > now-60 && second <= now {
			most = max(most, peak.values[slot])
		}
	}
	return most
}

// PeakNormalRequestsPerSecond is the most normal query permits asked for in
// one second of the last minute: the queries this process's owned groups
// made ready at once, whatever the permits then granted.
func (coordinator *FlightCoordinator) PeakNormalRequestsPerSecond() int {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.normalRequests.Peak(coordinator.now())
}
