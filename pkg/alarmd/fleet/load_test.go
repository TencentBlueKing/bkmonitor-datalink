// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"
	"time"
)

func intPtr(value int) *int { return &value }

// The on-time reading is the two-window rule: nothing overdue keeps up; overdue
// with the short window not below the long one is catching up; overdue with
// the short window worse is falling behind; no census cannot say. Rates are
// absent, not zero, over a window with no rounds.
func TestOnTimeIsTheTwoWindowReading(t *testing.T) {
	cases := []struct {
		name   string
		census *ScheduleCensus
		want   OnTimeState
	}{
		{"no census", nil, OnTimeUnknown},
		{"nothing overdue", &ScheduleCensus{Completed1h: 100, OnTime1h: 90, Completed6h: 600, OnTime6h: 590}, OnTimeKeepingUp},
		{"overdue, short window not worse", &ScheduleCensus{Overdue: 3, Completed1h: 100, OnTime1h: 99, Completed6h: 600, OnTime6h: 590}, OnTimeCatchingUp},
		{"overdue, short window worse", &ScheduleCensus{Overdue: 3, Completed1h: 100, OnTime1h: 80, Completed6h: 600, OnTime6h: 590}, OnTimeFallingBehind},
		{"overdue, quiet hour", &ScheduleCensus{Overdue: 3, Completed6h: 600, OnTime6h: 590}, OnTimeCatchingUp},
	}
	for _, tc := range cases {
		reading := onTimeOf(tc.census)
		if reading.State != tc.want {
			t.Errorf("%s: state = %s, want %s", tc.name, reading.State, tc.want)
		}
	}
	quiet := onTimeOf(&ScheduleCensus{Overdue: 3, Completed6h: 600, OnTime6h: 590})
	if quiet.Rate1h != nil || quiet.Rate6h == nil || *quiet.Rate6h < 98.3 || *quiet.Rate6h > 98.4 {
		t.Errorf("quiet hour rates = %v / %v, want the hour absent and six hours at 98.3%%", quiet.Rate1h, quiet.Rate6h)
	}
}

// The backlog reading compares the overdue count with the one the index found
// earlier. No earlier sample, or one younger than ten minutes, is not a
// trend; within the margin it is flat; past it, growing or shrinking.
func TestBacklogIsReadAgainstTheEarlierSample(t *testing.T) {
	cases := []struct {
		name    string
		census  *ScheduleCensus
		want    BacklogState
		earlier *int
	}{
		{"no census", nil, BacklogUnknown, nil},
		{"no earlier sample", &ScheduleCensus{Overdue: 5}, BacklogUnknown, nil},
		{"sample too young", &ScheduleCensus{Overdue: 5, OverdueAgo: intPtr(1), OverdueAgoSeconds: 300}, BacklogUnknown, intPtr(1)},
		{"none then none", &ScheduleCensus{Overdue: 0, OverdueAgo: intPtr(0), OverdueAgoSeconds: 3600}, BacklogNone, intPtr(0)},
		{"within the margin", &ScheduleCensus{Overdue: 6, OverdueAgo: intPtr(5), OverdueAgoSeconds: 3600}, BacklogFlat, intPtr(5)},
		{"growing", &ScheduleCensus{Overdue: 9, OverdueAgo: intPtr(5), OverdueAgoSeconds: 3600}, BacklogGrowing, intPtr(5)},
		{"shrinking", &ScheduleCensus{Overdue: 1, OverdueAgo: intPtr(5), OverdueAgoSeconds: 3600}, BacklogShrinking, intPtr(5)},
		// A tenth of a large backlog is the margin, not two.
		{"large, within a tenth", &ScheduleCensus{Overdue: 108, OverdueAgo: intPtr(100), OverdueAgoSeconds: 1800}, BacklogFlat, intPtr(100)},
		{"large, past a tenth", &ScheduleCensus{Overdue: 112, OverdueAgo: intPtr(100), OverdueAgoSeconds: 1800}, BacklogGrowing, intPtr(100)},
	}
	for _, tc := range cases {
		reading := backlogOf(tc.census)
		if reading.State != tc.want {
			t.Errorf("%s: state = %s, want %s (reading %+v)", tc.name, reading.State, tc.want, reading)
		}
		if (reading.Earlier == nil) != (tc.earlier == nil) || (tc.earlier != nil && *reading.Earlier != *tc.earlier) {
			t.Errorf("%s: earlier = %v, want %v", tc.name, reading.Earlier, tc.earlier)
		}
	}
}

// The bottleneck names the resource the evidence points at, most specific
// first, and names permits or the queue only when the work is behind: on a
// deployment keeping up, queued permits are what a full-enough deployment
// looks like. Behind with nothing pointing anywhere is its own answer -- the
// constraint is the schedule, and adding resources is not the move.
func TestBottleneckIsNamedFromTheEvidence(t *testing.T) {
	base := func() *CapacityView {
		return &CapacityView{PermitAcquires: 1000, PermitWaits: 100, ThrottledKnown: true, CPUSeconds: 1000, ThrottledSeconds: 10,
			Rotation: &Rotation{DeferredQueueFull: 0}}
	}
	cases := []struct {
		name   string
		shape  func(*CapacityView)
		behind bool
		want   Bottleneck
	}{
		{"keeping up, nothing", func(*CapacityView) {}, false, BottleneckNone},
		{"behind, nothing points anywhere", func(*CapacityView) {}, true, BottleneckUnlocated},
		{"budget rejections, even keeping up", func(c *CapacityView) { c.Rejections = map[string]uint64{"state": 3} }, false, BottleneckBudget},
		{"memory limit hit", func(c *CapacityView) { c.MemoryLimitHits = 1 }, false, BottleneckMemory},
		{"oom killed", func(c *CapacityView) { c.MemoryOOMKills = 1 }, false, BottleneckMemory},
		{"heavily throttled", func(c *CapacityView) { c.ThrottledSeconds = 300 }, false, BottleneckCPU},
		{"permits queued, keeping up", func(c *CapacityView) { c.PermitWaits = 700 }, false, BottleneckNone},
		{"permits queued, behind", func(c *CapacityView) { c.PermitWaits = 700 }, true, BottleneckPermits},
		{"queue full, behind", func(c *CapacityView) { c.Rotation.DeferredQueueFull = 12 }, true, BottleneckQueue},
		{"queue full, keeping up", func(c *CapacityView) { c.Rotation.DeferredQueueFull = 12 }, false, BottleneckNone},
		// Budget outranks a full queue: it is capacity by construction.
		{"budget and queue", func(c *CapacityView) { c.Rejections = map[string]uint64{"slot": 1}; c.Rotation.DeferredQueueFull = 12 }, true, BottleneckBudget},
	}
	for _, tc := range cases {
		capacity := base()
		tc.shape(capacity)
		if got := bottleneckOf(capacity, tc.behind, nil).Resource; got != tc.want {
			t.Errorf("%s: resource = %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := bottleneckOf(nil, true, nil).Resource; got != BottleneckUnknown {
		t.Errorf("no capacity: resource = %s, want UNKNOWN", got)
	}
	// The shares travel with the reading, since start, so the words can show
	// what the name was read from.
	reading := bottleneckOf(base(), false, nil)
	if reading.PermitWaitShare == nil || *reading.PermitWaitShare != 0.1 || reading.ThrottledShare == nil || *reading.ThrottledShare != 0.01 {
		t.Errorf("shares = %v / %v, want 0.1 permit waits and 0.01 throttled", reading.PermitWaitShare, reading.ThrottledShare)
	}
}

// The split outranks the permit and queue readings and gives way to the
// resource ones. A leader's round that would move objects says the ready
// replicas hold uneven shares; the loaded one's permits and queue are then
// full because of what it holds, so naming them would send a reader to add
// concurrency to a deployment whose other replica is idle. A budget
// rejection or a memory limit stays what it is, with the round beside it so
// the sentence can say whose reading it is. A round that moves nothing is
// not a skew and changes nothing.
func TestBottleneckReadsTheSplitBeforeTheResourcesItFills(t *testing.T) {
	skewed := &RebalanceFacts{ReadyWorkers: 2, Assigned: 2370, Target: 1185, MostOwned: 2370, LeastOwned: 0,
		MostOwnedBy: "alarmd-a", LeastOwnedBy: "alarmd-b", Batch: 23, PlannedMoves: 23, StopSpreadPercent: 5, Shadow: true}
	even := &RebalanceFacts{ReadyWorkers: 2, Assigned: 2370, Target: 1185, MostOwned: 1190, LeastOwned: 1180, Batch: 23, StopSpreadPercent: 5, Shadow: true}
	base := func() *CapacityView {
		return &CapacityView{PermitAcquires: 1000, PermitWaits: 700, CPUSeconds: 100, ThrottledSeconds: 1, ThrottledKnown: true, Rotation: &Rotation{DeferredQueueFull: 12}}
	}
	cases := []struct {
		name      string
		shape     func(*CapacityView)
		behind    bool
		rebalance *RebalanceFacts
		want      Bottleneck
		skew      bool
	}{
		{"behind, permits and queue full, skewed", func(*CapacityView) {}, true, skewed, BottleneckSkew, true},
		{"behind, permits and queue full, even", func(*CapacityView) {}, true, even, BottleneckPermits, false},
		{"keeping up, skewed", func(*CapacityView) {}, false, skewed, BottleneckNone, true},
		{"budget rejected, skewed", func(c *CapacityView) { c.Rejections = map[string]uint64{"slot": 1} }, true, skewed, BottleneckBudget, true},
		{"memory limit hit, skewed", func(c *CapacityView) { c.MemoryLimitHits = 1 }, true, skewed, BottleneckMemory, true},
		{"behind, nothing points anywhere, skewed", func(c *CapacityView) { c.PermitWaits = 0; c.Rotation.DeferredQueueFull = 0 }, true, skewed, BottleneckSkew, true},
	}
	for _, tc := range cases {
		capacity := base()
		tc.shape(capacity)
		reading := bottleneckOf(capacity, tc.behind, tc.rebalance)
		if reading.Resource != tc.want {
			t.Errorf("%s: resource = %s, want %s", tc.name, reading.Resource, tc.want)
		}
		if (reading.Skew != nil) != tc.skew {
			t.Errorf("%s: skew carried = %v, want %v", tc.name, reading.Skew != nil, tc.skew)
		}
		if reading.Skew != nil && (reading.Skew.MostOwnedBy != "alarmd-a" || reading.Skew.PlannedMoves != 23) {
			t.Errorf("%s: skew = %+v, want the leader's round whole", tc.name, reading.Skew)
		}
	}
	if reading := bottleneckOf(nil, true, skewed); reading.Resource != BottleneckUnknown || reading.Skew == nil {
		t.Errorf("no capacity, skewed: reading = %+v, want UNKNOWN with the round carried", reading)
	}
}

// The judgment as a whole: the four readings from one view, loss counted
// the way the first screen counts it, the bottleneck asked about when any of
// the other three says the work is behind, and the limits named -- always
// that no headroom is estimated.
func TestLoadIsTheFourReadingsWithTheirLimits(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	demoted := []Anomaly{{QueryGroup: "qg-refused", Kind: KindQueryCooldown,
		Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}}}
	Attribute(demoted, at)
	view := &View{
		Schedule: &ScheduleCensus{Overdue: 0, Completed1h: 100, OnTime1h: 99, Completed6h: 600, OnTime6h: 590,
			OverdueAgo: intPtr(0), OverdueAgoSeconds: 1800},
		Capacity: &CapacityView{PermitAcquires: 1000, PermitWaits: 700, Rotation: &Rotation{DeferredQueueFull: 12}},
		Demoted:  demoted,
		GapSkips: map[string]SkippedSpan{
			"qg-refused": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a"},
			"qg-losing":  {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-2 * time.Minute), Replica: "pod-a"},
			"qg-stopped": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Hour), Replica: "pod-a"},
		},
	}
	load := LoadOf(view, at)
	if load.OnTime.State != OnTimeKeepingUp || load.Backlog.State != BacklogNone {
		t.Errorf("on time = %s, backlog = %s, want KEEPING_UP and NONE", load.OnTime.State, load.Backlog.State)
	}
	if load.Loss.State != LossInProgress || load.Loss.Ongoing != 1 || load.Loss.WhileDemotedRecent != 1 ||
		load.Loss.WindowSeconds != int(RecentSkipWindow/time.Second) {
		t.Errorf("loss = %+v, want in progress: 1 ongoing, 1 while demoted, the window named", load.Loss)
	}
	// Loss in progress is what asks the bottleneck question here, and the
	// queued permits answer it.
	if load.Bottleneck.Resource != BottleneckPermits {
		t.Errorf("bottleneck = %s, want PERMITS: the work is losing rounds and 70%% of acquisitions queued", load.Bottleneck.Resource)
	}
	limits := map[LoadLimit]bool{}
	for _, limit := range load.Limits {
		limits[limit] = true
	}
	if !limits[LimitNoHeadroomEstimate] || !limits[LimitCountersSinceStart] || !limits[LimitTrendSpan] || limits[LimitNoCensus] || limits[LimitNoCapacity] {
		t.Errorf("limits = %v, want no headroom estimate, counters since start, and the half-hour trend span", load.Limits)
	}
	// Nothing to read from: every reading says so, and the limits name it.
	bare := LoadOf(&View{}, at)
	if bare.OnTime.State != OnTimeUnknown || bare.Backlog.State != BacklogUnknown || bare.Bottleneck.Resource != BottleneckUnknown || bare.Loss.State != LossNone {
		t.Errorf("bare load = %+v, want unknown on time, backlog and bottleneck, no loss", bare)
	}
	bareLimits := map[LoadLimit]bool{}
	for _, limit := range bare.Limits {
		bareLimits[limit] = true
	}
	if !bareLimits[LimitNoCensus] || !bareLimits[LimitNoCapacity] || !bareLimits[LimitNoHeadroomEstimate] {
		t.Errorf("bare limits = %v, want no census, no capacity, no headroom estimate", bare.Limits)
	}
}

// The earlier backlog adds across replicas only when every replica has one,
// over the shortest of their spans: a trend is only as long as its youngest
// sample, and one replica that just restarted makes the deployment's trend
// unknown rather than half of one.
func TestTheEarlierBacklogAggregatesOnlyWhenEveryReplicaHasOne(t *testing.T) {
	both := []Snapshot{
		{Replica: "pod-a", Schedule: &ScheduleCensus{Overdue: 3, OverdueAgo: intPtr(1), OverdueAgoSeconds: 3540}},
		{Replica: "pod-b", Schedule: &ScheduleCensus{Overdue: 4, OverdueAgo: intPtr(2), OverdueAgoSeconds: 1200}},
	}
	view := View{}
	aggregateSchedule(&view, both)
	if view.Schedule == nil || view.Schedule.Overdue != 7 || view.Schedule.OverdueAgo == nil || *view.Schedule.OverdueAgo != 3 ||
		view.Schedule.OverdueAgoSeconds != 1200 {
		t.Fatalf("aggregate = %+v, want overdue 7, earlier 3 over the shorter span of 1200 s", view.Schedule)
	}
	// One replica without a sample: the deployment has none.
	one := []Snapshot{
		{Replica: "pod-a", Schedule: &ScheduleCensus{Overdue: 3, OverdueAgo: intPtr(1), OverdueAgoSeconds: 3540}},
		{Replica: "pod-b", Schedule: &ScheduleCensus{Overdue: 4}},
	}
	for _, order := range [][]Snapshot{one, {one[1], one[0]}} {
		view = View{}
		aggregateSchedule(&view, order)
		if view.Schedule.OverdueAgo != nil || view.Schedule.OverdueAgoSeconds != 0 {
			t.Fatalf("aggregate with a replica lacking a sample = %+v, want no earlier backlog, whichever replica folds first", view.Schedule)
		}
	}
}
