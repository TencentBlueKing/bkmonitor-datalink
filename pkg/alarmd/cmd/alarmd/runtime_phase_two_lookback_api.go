package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

type lookbackGroupReading struct {
	QueryGroup     execution.QueryGroupIdentity `json:"query_group"`
	HoldKnown      bool                         `json:"hold_known"`
	ReadHoldMillis int64                        `json:"read_hold_ms"`
	ReadHold       *readhold.Record             `json:"read_hold,omitempty"`
	Annotation     string                       `json:"annotation,omitempty"`
	Supplement     *lookback.SupplementReading  `json:"supplement,omitempty"`
	Counters       *lookback.GroupReading       `json:"counters,omitempty"`
}

type lookbackGroupPage struct {
	Total  int                    `json:"total_owned"`
	Next   string                 `json:"next,omitempty"`
	Groups []lookbackGroupReading `json:"groups"`
}

func (holds *productionReadHolds) groupPage(engine *lookback.Engine, after string, limit int) lookbackGroupPage {
	holds.mu.Lock()
	groups := make([]execution.QueryGroupIdentity, 0, len(holds.groups))
	for qg := range holds.groups {
		groups = append(groups, qg)
	}
	holds.mu.Unlock()
	groups = slices.DeleteFunc(groups, func(qg execution.QueryGroupIdentity) bool {
		_, err := holds.owner(qg)
		return err != nil
	})
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	page := lookbackGroupPage{Total: len(groups), Groups: []lookbackGroupReading{}}
	start := sort.Search(len(groups), func(i int) bool { return string(groups[i]) > after })
	end := min(start+limit, len(groups))
	for _, qg := range groups[start:end] {
		inspection := holds.controller.Inspect(qg)
		// A record that did not decode says nothing until the group's next
		// write replaces it.
		record, known := inspection.Record, holdKnown(inspection)
		row := lookbackGroupReading{QueryGroup: qg, HoldKnown: known}
		if known {
			row.ReadHold = &record
			row.ReadHoldMillis = record.HoldMillis
			if record.PendingHoldMillis != nil {
				row.ReadHoldMillis = *record.PendingHoldMillis
			}
			row.Annotation = "alarmd 当前自动推后 " + strconv.FormatFloat(float64(row.ReadHoldMillis)/1000, 'f', -1, 64) + " 秒"
		}
		if engine != nil {
			if reading, found := engine.GroupReading(qg); found {
				row.Counters = &reading
			}
			if reading, found := engine.SupplementReading(qg); found {
				row.Supplement = &reading
			}
		}
		page.Groups = append(page.Groups, row)
	}
	if end < len(groups) && end > start {
		page.Next = string(groups[end-1])
	}
	return page
}

type lookbackAPIReading struct {
	cliLookbackReading
	WorkerID string `json:"worker_id"`
	lookbackGroupPage
}

// The answering replica reads its own bounded memory. Native APIs retain the
// same surface policy as the other fleet reads; no diagnostic Redis/CLI pool.
func withLookbackAPI(next http.Handler, engine *lookback.Engine, standing lookbackStanding, holds *productionReadHolds, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/lookback" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		limit := 200
		after := r.URL.Query().Get("after")
		if len(after) > 256 {
			http.Error(w, "after must be at most 256 bytes", http.StatusBadRequest)
			return
		}
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 200 {
				http.Error(w, "limit must be 1..200", http.StatusBadRequest)
				return
			}
			limit = parsed
		}
		reading := lookbackAPIReading{cliLookbackReading: cliLookbackReading{Scope: "answering_replica", ReadAt: now().UTC(), lookbackStanding: standing}}
		if engine != nil {
			stats := productionLookbackStats(engine, holds)
			reading.Stats = &stats
		}
		if holds != nil {
			reading.WorkerID = holds.cfg.PhaseTwo.Worker.ID
			reading.lookbackGroupPage = holds.groupPage(engine, after, limit)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reading)
	})
}

func productionLookbackStats(engine *lookback.Engine, holds *productionReadHolds) lookback.Stats {
	stats := engine.Stats()
	if holds != nil {
		stats.ReadHoldTransitions, stats.ReadHoldTransitionOvertaken = holds.transitions.Load(), holds.overtaken.Load()
		// Every reason and source at zero: a series that is absent reads as
		// a build without it.
		controller := holds.controller.Stats()
		stats.ReadHoldPredecessors = make(map[string]uint64, len(readhold.PredecessorReasons)+len(readhold.LinkSkipReasons))
		for _, reason := range readhold.PredecessorReasons {
			stats.ReadHoldPredecessors[reason] = controller.Predecessors[reason]
		}
		holds.linksMu.Lock()
		for _, reason := range readhold.LinkSkipReasons {
			stats.ReadHoldPredecessors[reason] = holds.links[reason]
		}
		holds.linksMu.Unlock()
		stats.ReadHoldClamped = map[string]uint64{readhold.ClampKnown: controller.Clamped[readhold.ClampKnown],
			readhold.ClampFallback: controller.Clamped[readhold.ClampFallback]}
		stats.ReadHoldOwnCorrupt, stats.ReadHoldRetireCloseFailed = controller.OwnCorrupt, holds.retireCloseFailed.Load()
		stats.ReadHoldCloseSkipped = holds.closeSkipped.Load()
		stats.ReadHoldGroups = holds.groupsBySource(engine)
		stats.ReadHoldDegraded = make(map[string]uint64, len(readhold.DegradedReasons))
		holds.degradedMu.Lock()
		for _, reason := range readhold.DegradedReasons {
			stats.ReadHoldDegraded[reason] = holds.degraded[reason]
		}
		holds.degradedMu.Unlock()
	}
	return stats
}

// groupsBySource counts the Query Groups this process holds by their
// source and read hold, from the same state the lookback's group page reads,
// with the hold known as that page knows it. A group the lookback has not
// seen yet is under other. Nil when it holds none.
func (holds *productionReadHolds) groupsBySource(engine *lookback.Engine) map[string]lookback.ReadHoldGroups {
	holds.mu.Lock()
	groups := make([]execution.QueryGroupIdentity, 0, len(holds.groups))
	for qg := range holds.groups {
		groups = append(groups, qg)
	}
	holds.mu.Unlock()
	groups = slices.DeleteFunc(groups, func(qg execution.QueryGroupIdentity) bool {
		_, err := holds.owner(qg)
		return err != nil
	})
	if len(groups) == 0 {
		return nil
	}
	sources := engine.GroupSources(groups)
	counts := make(map[string]lookback.ReadHoldGroups)
	for _, qg := range groups {
		source := sources[qg]
		if source == "" {
			source = lookback.SourceOther
		}
		entry := counts[source]
		inspection := holds.controller.Inspect(qg)
		if !holdKnown(inspection) {
			entry.Unknown++
			counts[source] = entry
			continue
		}
		hold := inspection.Record.HoldMillis
		if inspection.Record.PendingHoldMillis != nil {
			hold = *inspection.Record.PendingHoldMillis
		}
		if hold > 0 {
			entry.Held++
		}
		if inspection.Record.AtLimit {
			entry.AtLimit++
		}
		if !entry.MaxKnown || hold > entry.MaxMillis {
			entry.MaxMillis, entry.MaxKnown = hold, true
		}
		counts[source] = entry
	}
	return counts
}

// holdOf is one Query Group's read hold as the lookback's group page knows
// it: unknown while its record is unread or corrupt.
func (holds *productionReadHolds) holdOf(queryGroup string) (int64, bool) {
	inspection := holds.controller.Inspect(execution.QueryGroupIdentity(queryGroup))
	if !holdKnown(inspection) {
		return 0, false
	}
	if inspection.Record.PendingHoldMillis != nil {
		return *inspection.Record.PendingHoldMillis, true
	}
	return inspection.Record.HoldMillis, true
}

func (holds *productionReadHolds) fleetFacts() map[string]fleet.ReadHoldFacts {
	holds.mu.Lock()
	groups := make([]execution.QueryGroupIdentity, 0, len(holds.groups))
	bases := make(map[execution.QueryGroupIdentity]readHoldBasis, len(holds.groups))
	for qg, owned := range holds.groups {
		groups = append(groups, qg)
		bases[qg] = readHoldBasis{delay: owned.queryDelay, step: owned.queryStep, unit: owned.delayUnit, settlingWait: owned.settlingWait,
			known: owned.settled && owned.queryRoute != ""}
	}
	holds.mu.Unlock()
	facts := make(map[string]fleet.ReadHoldFacts)
	for _, qg := range groups {
		if _, err := holds.owner(qg); err != nil {
			continue
		}
		inspection := holds.controller.Inspect(qg)
		if !holdKnown(inspection) {
			// Listed as such: left out, the group would read as holding
			// nothing (fleet.ReadHoldFacts).
			facts[string(qg)] = fleet.ReadHoldFacts{Unknown: true, Annotation: "alarmd 当前的推后未知"}
			continue
		}
		if inspection.Missing {
			// No record: the group holds nothing, which is what leaving it out
			// says.
			continue
		}
		record := inspection.Record
		millis := record.HoldMillis
		if record.PendingHoldMillis != nil {
			millis = *record.PendingHoldMillis
		}
		entry := fleet.ReadHoldFacts{Millis: millis, ArrivalAgeMillis: record.ArrivalAgeMillis, LimitMillis: record.LimitMillis,
			AtLimit: record.AtLimit, RaisedAfterLowering: record.RaisedAfterLowering, NoWholeWindowArrival: record.Noise, Rung: record.Rung,
			Buckets:    append([]int64(nil), record.Buckets[:min(len(record.Buckets), fleet.MaxReadEarlyBuckets)]...),
			Annotation: "alarmd 当前自动推后 " + strconv.FormatFloat(float64(millis)/1000, 'f', -1, 64) + " 秒"}
		if record.HoldMillis > 0 {
			entry.HeldSince = int64(record.SinceSlot)
		}
		if basis := bases[qg]; basis.known {
			entry.DelaySeconds = int64(basis.delay / time.Second)
			entry.SettlingWaitSeconds = int64(basis.settlingWait / time.Second)
			if millis > 0 {
				entry.SuggestedDelaySeconds = basis.suggestion(record)
			}
		}
		facts[string(qg)] = entry
	}
	return facts
}

// readHoldBasis is what a Query Group's spec says of its reads: the
// time_delay its query runs under, its data step, and the settling wait the
// hold is reckoned beyond.
type readHoldBasis struct {
	delay, step, settlingWait time.Duration
	// unit is what the delay was rounded to and the suggestion is: the data
	// step, or a shorter schedule step for a query read unaligned.
	unit  time.Duration
	known bool
}

// suggestion is the time_delay at which the record's arrival age needs no
// hold, aligned up to the data step; zero when it is not past the delay.
// Only a measured arrival age suggests: the record's is written by the
// group's own early reads, the largest they found, or by a lowering, which
// rests on three matching earlier reads (readhold, Lowered) -- both
// measured. A hold that is a predecessor's bound or a degraded freeze
// writes none, and suggests nothing.
func (basis readHoldBasis) suggestion(record readhold.Record) int64 {
	if record.ArrivalAgeMillis <= 0 {
		return 0
	}
	need := record.ArrivalAgeMillis - basis.settlingWait.Milliseconds()
	if need <= basis.delay.Milliseconds() {
		return 0
	}
	seconds := (need + 999) / 1000
	unit := basis.unit
	if unit <= 0 {
		unit = basis.step
	}
	if step := int64(unit / time.Second); step > 0 {
		seconds = (seconds + step - 1) / step * step
	}
	return seconds
}

// holdKnown is whether a Query Group's read hold is known from its record:
// one that was read and decoded, or none at all -- a group whose hold has
// only ever been zero keeps none, and the controller reads a missing record
// as zero. Unknown is a record not read yet, or one that did not decode.
//
// One group reads zero here and may not be: a group never prepared, whose
// Plan moved from a group that held, can be held by the transition its first
// prepare seeds; until that first Slot it reads zero.
func holdKnown(inspection readhold.Inspection) bool {
	return inspection.Loaded && !inspection.Corrupt
}
