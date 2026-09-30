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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The shape a new deployment showed: the source listed strategies and the
// round accepted none, most for want of identity, some without a document.
func blockedSource(at time.Time) *SourceFacts {
	withheld := []WithheldObject{
		{StrategyID: "120", Scope: "STRATEGY", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_OBJECT_INCOMPLETE"},
		{StrategyID: "9", Scope: "STRATEGY", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"},
	}
	for index := 0; index < 24; index++ {
		withheld = append(withheld, WithheldObject{StrategyID: strconv.Itoa(100 + index), Scope: "STRATEGY",
			Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"})
	}
	return NewSourceFacts(at, map[string]int{"SOURCE_INCOMPLETE": 26, "ACCEPTED": 0}, withheld)
}

func TestSourceFactsFoldTheRoundLargestGroupFirstWithABoundedNumericSample(t *testing.T) {
	facts := blockedSource(now)
	if facts.Listed != 26 || facts.Accepted != 0 || !facts.Blocked() {
		t.Fatalf("listed %d accepted %d blocked %v, want 26/0/true", facts.Listed, facts.Accepted, facts.Blocked())
	}
	if _, zero := facts.Objects["ACCEPTED"]; zero {
		t.Errorf("a zero disposition is carried: %v", facts.Objects)
	}
	if len(facts.Withheld) != 2 || facts.Withheld[0].Reason != "SOURCE_IDENTITY_UNAVAILABLE" || facts.Withheld[0].Count != 25 ||
		facts.Withheld[1].Reason != "SOURCE_OBJECT_INCOMPLETE" || facts.Withheld[1].Count != 1 {
		t.Fatalf("withheld groups = %+v, want identity 25 then object 1", facts.Withheld)
	}
	sample := facts.Withheld[0].Samples
	if len(sample) != SourceSampleLimit {
		t.Fatalf("sample has %d entries, want the limit %d with %d more counted", len(sample), SourceSampleLimit, 25-SourceSampleLimit)
	}
	// Numeric order: 9 before 100, not after 123 as a string sort would put it.
	if sample[0].StrategyID != "9" || sample[1].StrategyID != "100" || sample[19].StrategyID != "118" {
		t.Errorf("sample order = %s, %s ... %s; want 9, 100 ... 118", sample[0].StrategyID, sample[1].StrategyID, sample[19].StrategyID)
	}
	// Partially withheld is not blocked: some detection is happening.
	partial := NewSourceFacts(now, map[string]int{"ACCEPTED": 3, "SOURCE_INCOMPLETE": 1},
		[]WithheldObject{{StrategyID: "7", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"}})
	if partial.Blocked() || partial.WithheldCount("SOURCE_INCOMPLETE") != 1 {
		t.Errorf("a source with accepted strategies reads as blocked: %+v", partial)
	}
	// An empty source is not blocked either: nothing listed is nothing withheld.
	if NewSourceFacts(now, map[string]int{}, nil).Blocked() {
		t.Error("an empty source reads as blocked")
	}
	var none *SourceFacts
	if none.Blocked() || none.WithheldCount("SOURCE_INCOMPLETE") != 0 || none.Groups("SOURCE_INCOMPLETE") != nil {
		t.Error("a nil source does not read as nothing")
	}
}

// idleSnapshots is the deployment a blocked source produces: replicas that
// own nothing, because nothing was accepted for them to own.
func idleSnapshots() []Snapshot {
	return []Snapshot{
		{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second)},
		{Replica: "pod-b", TakenAt: now.Add(-10 * time.Second)},
	}
}

func TestABlockedSourceDegradesTheVerdictAndNamesItself(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].Source = blockedSource(now.Add(-30 * time.Second))
	// The other replica led earlier and still carries its round, which
	// accepted everything: the newest round is what the source is now.
	snapshots[1].Source = NewSourceFacts(now.Add(-10*time.Minute), map[string]int{"ACCEPTED": 26}, nil)
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	if view.Source == nil || view.SourceReplica != "pod-a" || view.Source.Listed != 26 {
		t.Fatalf("source = %+v from %q, want pod-a's round", view.Source, view.SourceReplica)
	}
	if view.Health != HealthDegraded {
		t.Fatalf("health = %s, want DEGRADED for a source that lists strategies and accepts none (degradations %+v)", view.Health, view.Degradations)
	}
	var blocked *Degradation
	for index := range view.Degradations {
		if view.Degradations[index].Kind == DegradationSourceBlocked {
			blocked = &view.Degradations[index]
		}
	}
	if blocked == nil || blocked.Replica != "pod-a" {
		t.Fatalf("no SOURCE_BLOCKED degradation on pod-a: %+v", view.Degradations)
	}
	want := "source lists 26 strategies, 0 accepted: 25 SOURCE_INCOMPLETE/SOURCE_IDENTITY_UNAVAILABLE, 1 SOURCE_INCOMPLETE/SOURCE_OBJECT_INCOMPLETE"
	if blocked.Text != want {
		t.Errorf("degradation text = %q, want %q", blocked.Text, want)
	}
	// The newest round accepting everything: no degradation, HEALTHY.
	snapshots[1].Source.At = now
	snapshots[0].Owned, snapshots[0].Determined, snapshots[1].Owned, snapshots[1].Determined = 13, 13, 13, 13
	view = Aggregate(Expectation{QueryGroups: 26, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthHealthy || view.SourceReplica != "pod-b" {
		t.Errorf("health = %s from %q, want HEALTHY on pod-b's newer accepting round (degradations %+v)", view.Health, view.SourceReplica, view.Degradations)
	}
}

func TestTheSourceStandingsFoldWithheldGroupsUnderTheirOwners(t *testing.T) {
	at := now
	source := NewSourceFacts(at, map[string]int{"ACCEPTED": 5, "SOURCE_INCOMPLETE": 2, "CONFIG_REJECTED": 1, "STALE_CONFIG": 1, "UNSUPPORTED_PHASE2_CAPABILITY": 1},
		[]WithheldObject{
			{StrategyID: "1", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"},
			{StrategyID: "2", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_OBJECT_INCOMPLETE"},
			{StrategyID: "3", Scope: "LEVEL", LevelID: 1, Disposition: "CONFIG_REJECTED", Reason: "LEVEL_INVALID", FieldPath: "items[0]"},
			{StrategyID: "4", Disposition: "STALE_CONFIG", Reason: "LEVEL_INVALID"},
			{StrategyID: "5", Disposition: "UNSUPPORTED_PHASE2_CAPABILITY", Reason: "SNAPSHOT_RETENTION_INSUFFICIENT"},
		})
	view := &View{Source: source, SourceReplica: "pod-a",
		// A blocked-source degradation must not also become a REPLICA_DEGRADED fold.
		Degradations: []Degradation{{Kind: DegradationSourceBlocked, Replica: "pod-a"}}}
	reports := ReportChecks(nil, nil, view, at)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	if _, folded := byCode[CheckReplicaDegraded]; folded {
		t.Errorf("SOURCE_BLOCKED was folded under REPLICA_DEGRADED as well as under the source lines")
	}
	if len(reports) != 3 || reports[0].Code != CheckSourceIncomplete || reports[1].Code != CheckCapabilityUnsupported || reports[2].Code != CheckConfigRejected {
		t.Fatalf("reports = %v, want the three source standings in order", codes(reports))
	}
	incomplete := byCode[CheckSourceIncomplete]
	if incomplete.Owner != OwnerPlatform || incomplete.Strategies != 2 || incomplete.Objects != 0 || incomplete.LineCount() != 2 || incomplete.Replica != "pod-a" {
		t.Errorf("SOURCE_INCOMPLETE = owner %s strategies %d objects %d line %d replica %q; want PLATFORM 2 0 2 pod-a",
			incomplete.Owner, incomplete.Strategies, incomplete.Objects, incomplete.LineCount(), incomplete.Replica)
	}
	if len(incomplete.Groups) != 2 || incomplete.Groups[0].Key != "SOURCE_IDENTITY_UNAVAILABLE" || incomplete.Groups[0].Strategies != 1 ||
		incomplete.Groups[0].Disposition != "SOURCE_INCOMPLETE" || len(incomplete.Groups[0].Samples) != 1 || incomplete.Groups[0].Samples[0].StrategyID != "1" {
		t.Errorf("SOURCE_INCOMPLETE groups = %+v", incomplete.Groups)
	}
	rejected := byCode[CheckConfigRejected]
	if rejected.Owner != OwnerStrategy || rejected.Strategies != 2 || len(rejected.Groups) != 2 {
		t.Fatalf("CONFIG_REJECTED = owner %s strategies %d groups %+v", rejected.Owner, rejected.Strategies, rejected.Groups)
	}
	keys := map[string]CheckGroup{}
	for _, group := range rejected.Groups {
		keys[group.Key] = group
	}
	if _, stale := keys["STALE_CONFIG/LEVEL_INVALID"]; !stale {
		t.Errorf("the stale fold is not keyed apart from the rejected one: %v", keys)
	}
	if level := keys["LEVEL_INVALID"]; len(level.Samples) != 1 || level.Samples[0].FieldPath != "items[0]" || level.Samples[0].LevelID != 1 {
		t.Errorf("the compiler refusal lost its field or level: %+v", level.Samples)
	}
	if byCode[CheckCapabilityUnsupported].Owner != OwnerAlarmd {
		t.Errorf("CAPABILITY_UNSUPPORTED owner = %s, want ALARMD", byCode[CheckCapabilityUnsupported].Owner)
	}
	// The first screen's arithmetic: the platform's line is to act on, the
	// strategy's is governance, and neither adds objects.
	todo := SummarizeTodo(reports, nil, view, at)
	if todo.Checks != 2 || todo.Governance != 1 || todo.Objects != 0 {
		t.Errorf("todo = checks %d governance %d objects %d, want 2 1 0", todo.Checks, todo.Governance, todo.Objects)
	}
}

// The line's count is the records under it, not the groups: twenty-five
// strategies under one reason and one under another are twenty-six on the
// line and on its metric.
func TestASourceStandingCountsRecordsNotGroups(t *testing.T) {
	view := &View{Source: blockedSource(now), SourceReplica: "pod-a"}
	reports := ReportChecks(nil, nil, view, now)
	if len(reports) != 1 || reports[0].Code != CheckSourceIncomplete {
		t.Fatalf("reports = %v", codes(reports))
	}
	if reports[0].Strategies != 26 || reports[0].LineCount() != 26 || len(reports[0].Groups) != 2 {
		t.Errorf("strategies %d line %d groups %d, want 26 26 2", reports[0].Strategies, reports[0].LineCount(), len(reports[0].Groups))
	}
}

func codes(reports []CheckReport) []string {
	list := make([]string, 0, len(reports))
	for _, report := range reports {
		list = append(list, string(report.Code))
	}
	return list
}

func TestTheVerdictRouteCarriesTheSourceAndTheDependencies(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].Source = blockedSource(now.Add(-30 * time.Second))
	db := 8
	snapshots[0].Dependencies = []Endpoint{{Role: EndpointStateRedis, Kind: "redis", Address: "redis.example:6379", DB: &db, Configured: true}}
	snapshots[1].Dependencies = []Endpoint{{Role: EndpointStateRedis, Kind: "redis", Address: "other.example:6379", DB: &db, Configured: true}}
	snapshots[1].TakenAt = now.Add(-20 * time.Second)
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 0, Known: true}, replicas())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Health              string       `json:"health"`
		Source              *SourceFacts `json:"source"`
		SourceReplica       string       `json:"source_replica"`
		Dependencies        []Endpoint   `json:"dependencies"`
		DependenciesReplica string       `json:"dependencies_replica"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Health != string(HealthDegraded) || body.Source == nil || body.Source.Listed != 26 || body.SourceReplica != "pod-a" {
		t.Errorf("health %s source %+v from %q; want DEGRADED with pod-a's 26-strategy round", body.Health, body.Source, body.SourceReplica)
	}
	// The newest snapshot's dependencies: pod-a's, taken later than pod-b's.
	if len(body.Dependencies) != 1 || body.Dependencies[0].Address != "redis.example:6379" || body.DependenciesReplica != "pod-a" {
		t.Errorf("dependencies = %+v from %q, want pod-a's", body.Dependencies, body.DependenciesReplica)
	}
	// A build that publishes neither sends null for the round and an empty
	// list for the coordinates, not a missing field.
	handler = handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	text := recorder.Body.String()
	if !strings.Contains(text, `"source":null`) || !strings.Contains(text, `"dependencies":[]`) {
		t.Errorf("a snapshot without the facts does not send them as absent: %s", text)
	}
}

// The todo names the platform's lines apart from this deployment's, with the
// strategies under them: they are in Checks, and "alarmd 已确认 N 类" with a
// platform line among them tells the reader the wrong thing. The page used
// to derive the split by walking the lines; a reader of the JSON had nothing
// to read, and now reads what the page reads.
func TestTheTodoNamesThePlatformsLinesApart(t *testing.T) {
	view := &View{Source: blockedSource(now), SourceReplica: "pod-a"}
	reports := ReportChecks(nil, nil, view, now)
	todo := SummarizeTodo(reports, nil, view, now)
	if todo.Checks != 1 || todo.PlatformChecks != 1 || todo.PlatformStrategies != 26 {
		t.Fatalf("todo = checks %d, platform checks %d, platform strategies %d; want 1, 1, 26: the one line is the platform's, over 26 strategies",
			todo.Checks, todo.PlatformChecks, todo.PlatformStrategies)
	}
	// A deployment's own standing beside it: two lines, one of them the
	// platform's, and the platform count does not grow with ours.
	view.Degradations = []Degradation{{Kind: DegradationOpenAlertSetStale, Replica: "pod-a"}}
	reports = ReportChecks(nil, nil, view, now)
	todo = SummarizeTodo(reports, nil, view, now)
	if todo.Checks != 2 || todo.PlatformChecks != 1 || todo.PlatformStrategies != 26 {
		t.Fatalf("todo = checks %d, platform checks %d, platform strategies %d; want 2, 1, 26", todo.Checks, todo.PlatformChecks, todo.PlatformStrategies)
	}
}

// A source accepting nothing degrades the verdict only when nothing runs
// because of it. A deployment running objects on its last accepted
// configuration is detecting; what it has is a cache that cannot update the
// run, and the standing says so in two sentences -- what is running, what the
// cache is -- with the writer's marker as evidence of when something was last
// written and nothing more. Nothing running and nothing accepted is still the
// blocked deployment the degradation exists for.
func TestASourceAcceptingNothingDegradesOnlyWhenNothingRunsBecauseOfIt(t *testing.T) {
	age := int64(53356)
	unusable := func(at time.Time) *SourceFacts {
		withheld := make([]WithheldObject, 0, 67)
		for index := 0; index < 67; index++ {
			withheld = append(withheld, WithheldObject{StrategyID: strconv.Itoa(300 + index), Scope: "STRATEGY",
				Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE", FieldPath: "bk_tenant_id,space_uid"})
		}
		facts := NewSourceFacts(at, map[string]int{"SOURCE_INCOMPLETE": 67}, withheld)
		facts.ChangeSignalPresent, facts.ChangeSignalAgeSeconds = true, &age
		return facts
	}
	// Six objects running on the last accepted configuration.
	running := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 6, Determined: 6, Source: unusable(now.Add(-time.Minute))}}
	view := Aggregate(Expectation{QueryGroups: 6, Known: true}, running, []string{"pod-a"}, now, freshness)
	if view.Health != HealthHealthy {
		t.Fatalf("health = %s with 6 objects running on the last accepted configuration, want HEALTHY (degradations %+v)", view.Health, view.Degradations)
	}
	for _, degradation := range view.Degradations {
		if degradation.Kind == DegradationSourceBlocked {
			t.Fatalf("SOURCE_BLOCKED on a deployment running 6 objects: %+v", degradation)
		}
	}
	standing := view.SourceStanding
	if standing == nil || standing.Kind != SourceUpdateUnusable || standing.Listed != 67 || standing.Accepted != 0 ||
		standing.Incomplete != 67 || standing.Executing != 6 || standing.WriterAgeSeconds == nil || *standing.WriterAgeSeconds != 53356 {
		t.Fatalf("standing = %+v, want UPDATE_UNUSABLE over 67 listed / 0 accepted / 67 incomplete, 6 executing, marker 53356 s", standing)
	}
	if standing.Run != "6 个对象按已生效的配置继续检测" ||
		standing.Cache != "当前缓存有 67 条身份不完整，不能用于更新配置；缓存最近一次写入在 14.8 小时前（只说明没有新写入）" {
		t.Errorf("sentences = %q / %q", standing.Run, standing.Cache)
	}
	// The same source with nothing running: blocked, degraded, and said so.
	idle := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Source: unusable(now.Add(-time.Minute))}}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, idle, []string{"pod-a"}, now, freshness)
	if view.Health != HealthDegraded || view.SourceStanding == nil || view.SourceStanding.Kind != SourceBlocked {
		t.Fatalf("health = %s standing %+v with nothing running, want DEGRADED / BLOCKED", view.Health, view.SourceStanding)
	}
	if view.SourceStanding.Run != "没有任何策略在检测" ||
		view.SourceStanding.Cache != "策略缓存列出 67 条身份不完整，一条都不能用；缓存最近一次写入在 14.8 小时前（只说明没有新写入）" {
		t.Errorf("blocked sentences = %q / %q", view.SourceStanding.Run, view.SourceStanding.Cache)
	}
	// A mixture counts the incomplete and the rest apart -- the rest are on
	// other lines with other owners -- and none incomplete sends the reader
	// to those lines; neither borrows the all-incomplete word.
	mixed := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 3, Determined: 3,
		Source: NewSourceFacts(now, map[string]int{"SOURCE_INCOMPLETE": 7, "CONFIG_REJECTED": 3}, nil)}}
	view = Aggregate(Expectation{QueryGroups: 3, Known: true}, mixed, []string{"pod-a"}, now, freshness)
	if s := view.SourceStanding; s == nil || s.Kind != SourceUpdateUnusable ||
		s.Cache != "当前缓存有 10 条，其中 7 条身份不完整、3 条因别的原因被扣（原因见检查项），不能用于更新配置" {
		t.Errorf("mixed standing = %+v", s)
	}
	rejected := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second),
		Source: NewSourceFacts(now, map[string]int{"CONFIG_REJECTED": 4}, nil)}}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, rejected, []string{"pod-a"}, now, freshness)
	if s := view.SourceStanding; s == nil || s.Kind != SourceBlocked ||
		s.Cache != "策略缓存列出 4 条，全部因别的原因被扣（原因见检查项），一条都不能用" {
		t.Errorf("none-incomplete standing = %+v", s)
	}
	// What counts as running is the catalogue's count or what is owned,
	// whichever is more: six in the catalogue nobody owns yet still run.
	view = Aggregate(Expectation{QueryGroups: 6, Known: true}, idle, []string{"pod-a"}, now, freshness)
	if view.SourceStanding == nil || view.SourceStanding.Kind != SourceUpdateUnusable || view.SourceStanding.Executing != 6 {
		t.Errorf("standing with 6 expected and none owned = %+v, want UPDATE_UNUSABLE over 6", view.SourceStanding)
	}
	// And with the catalogue unknown, what is owned.
	view = Aggregate(Expectation{Known: false}, running, []string{"pod-a"}, now, freshness)
	if view.SourceStanding == nil || view.SourceStanding.Kind != SourceUpdateUnusable || view.SourceStanding.Executing != 6 {
		t.Errorf("standing with the catalogue unknown and 6 owned = %+v, want UPDATE_UNUSABLE over 6", view.SourceStanding)
	}
	// A source accepting some: accepting, with the withheld counted; one
	// listing nothing: nothing listed. Neither has a marker to speak of.
	accepting := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 70, Determined: 70,
		Source: NewSourceFacts(now, map[string]int{"ACCEPTED": 70, "SOURCE_INCOMPLETE": 11}, nil)}}
	view = Aggregate(Expectation{QueryGroups: 70, Known: true}, accepting, []string{"pod-a"}, now, freshness)
	if s := view.SourceStanding; s == nil || s.Kind != SourceAccepting || s.Run != "70 个对象正在检测" || s.Cache != "策略缓存列出 81 条，可用 70 条，扣住 11 条（原因见检查项）" {
		t.Errorf("accepting standing = %+v", s)
	}
	// A normalized record annotates a Plan that is also accepted: the
	// control plane records the Plan ACCEPTED and names what it read
	// otherwise beside it. The listed count takes the strategy once, the
	// refusal keeps its own count, and the normalized ones say so on their
	// own clause -- they are detecting.
	widened := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 70, Determined: 70,
		Source: NewSourceFacts(now, map[string]int{"ACCEPTED": 70, "SOURCE_INCOMPLETE": 11, "CONFIG_NORMALIZED": 2},
			[]WithheldObject{
				{StrategyID: "4108", Scope: "LEVEL", LevelID: 1, Disposition: "CONFIG_NORMALIZED", Reason: "EFFECTIVE_TIME_RANGE_INVALID"},
				{StrategyID: "4109", Scope: "LEVEL", LevelID: 1, Disposition: "CONFIG_NORMALIZED", Reason: "EFFECTIVE_TIME_RANGE_INVALID"},
				{StrategyID: "4110", Scope: "STRATEGY", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"}})}}
	view = Aggregate(Expectation{QueryGroups: 70, Known: true}, widened, []string{"pod-a"}, now, freshness)
	if s := view.SourceStanding; s == nil || s.Normalized != 2 ||
		s.Cache != "策略缓存列出 81 条，可用 70 条，扣住 11 条（原因见检查项），可用的里有 2 条的读法和配置写的不同（在检测，不是被扣，原因见检查项）" {
		t.Errorf("accepting standing with normalized records = %+v", s)
	}
	empty := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Source: NewSourceFacts(now, map[string]int{}, nil)}}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, empty, []string{"pod-a"}, now, freshness)
	if s := view.SourceStanding; s == nil || s.Kind != SourceNothingListed || s.Cache != "策略缓存里没有列出任何策略" || s.Run != "0 个对象正在检测" {
		t.Errorf("nothing-listed standing = %+v", s)
	}
	if view.Health == HealthDegraded {
		t.Errorf("a source listing nothing degraded the verdict: %+v", view.Degradations)
	}
	// No round: no standing.
	view = Aggregate(Expectation{QueryGroups: 6, Known: true}, healthySnapshots(), replicas(), now, freshness)
	if view.SourceStanding != nil {
		t.Errorf("standing without a source round = %+v, want none", view.SourceStanding)
	}
}

// The verdict route carries the standing beside the source facts, as
// published, so the page's two sentences are the server's.
func TestTheVerdictRouteCarriesTheSourceStanding(t *testing.T) {
	snapshots := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 6, Determined: 6,
		Source: NewSourceFacts(now.Add(-time.Minute), map[string]int{"SOURCE_INCOMPLETE": 67},
			[]WithheldObject{{StrategyID: "300", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE"}})}}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 6, Known: true}, []string{"pod-a"})
	_, health := get(t, handler, "/api/health")
	standing, _ := health["source_standing"].(map[string]any)
	if standing == nil || standing["kind"] != "UPDATE_UNUSABLE" || standing["executing"] != 6.0 || standing["incomplete"] != 67.0 ||
		standing["run"] != "6 个对象按已生效的配置继续检测" || standing["cache"] != "当前缓存有 67 条身份不完整，不能用于更新配置" {
		t.Fatalf("source_standing = %v", health["source_standing"])
	}
	if health["health"] != "HEALTHY" {
		t.Errorf("health = %v, want HEALTHY: the objects run", health["health"])
	}
}
