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
	"fmt"
	"sort"
	"time"
)

// SourceFacts is what the control leader's last refresh round found at the
// strategy source, before any object reached the fleet.
//
// The fleet's columns hold objects that were accepted into the Active Set. A
// strategy the source listed and the round withheld -- a document without the
// identity the contract requires, a document that is not there, a query the
// compiler refused -- is in no column, so a deployment whose source withholds
// every strategy used to read as a deployment with nothing to do: HEALTHY,
// expected 0. The counts existed as metrics and the names as log lines; this
// is the copy the page can read.
//
// Everything here is derived from one round's CatalogComposition. It holds
// strategy identifiers and nothing from the documents.
type SourceFacts struct {
	// At is when the round that produced these facts ran.
	At time.Time `json:"at"`
	// Listed is how many strategies the source listed: every object the round
	// recorded a disposition for, save the records that only annotate an
	// accepted one (CONFIG_NORMALIZED). Accepted is how many of them became
	// Plans.
	Listed   int `json:"listed"`
	Accepted int `json:"accepted"`
	// Objects is the partition of Listed by disposition, non-zero entries only.
	Objects map[string]int `json:"objects"`
	// Withheld is every (disposition, reason) pair that kept a strategy out,
	// with its count and a bounded sample of the strategies under it, largest
	// first. The reason is carried as the control plane wrote it.
	Withheld []WithheldGroup `json:"withheld"`
	// ChangeSignalPresent says the source offered its change marker this
	// round, and ChangeSignalAgeSeconds how long ago the writer moved it.
	// Absent age with a present marker is a marker this build could not read.
	ChangeSignalPresent    bool   `json:"change_signal_present"`
	ChangeSignalAgeSeconds *int64 `json:"change_signal_age_seconds,omitempty"`
	// Plans is how many Plans the round's Catalog holds and RevisionedPlans
	// how many of them carry an authoritative strategy revision -- the fact
	// that decides, under the automatic protocol choice, whether any event
	// can go out as the standard raw event. Zero revisioned on a source that
	// lists strategies is a deployment whose standard output path is
	// unreachable, and that has to be one number on the first screen rather
	// than a gate counter that only ever says not gated. Both absent (zero)
	// on a build before they existed; PlansKnown says the round reported
	// them.
	Plans           int  `json:"plans,omitempty"`
	RevisionedPlans int  `json:"revisioned_plans,omitempty"`
	PlansKnown      bool `json:"plans_known,omitempty"`
	// StandardPlans is how many of the Plans publish the standard raw event
	// -- the events the alert link turns into alerts, and so the alerts only
	// the link's Console can close. Read with PlansKnown.
	StandardPlans int `json:"standard_plans,omitempty"`
	// Set is the account of the source's active set across rounds -- what
	// one round's dispositions cannot say: which strategies are under grace
	// and since when, and how often the set drops strategies and lists them
	// again, by the hour. Absent on a build before it.
	Set *SourceSetFacts `json:"set,omitempty"`
}

// WithheldGroup is one (disposition, reason) pair the round withheld
// strategies under.
type WithheldGroup struct {
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Count       int    `json:"count"`
	// Samples names up to SourceSampleLimit of the strategies, smallest
	// identifier first so the same round always names the same ones. Count
	// above len(Samples) is the rest; a reader can tell a full list from a cut
	// one without a second field.
	Samples []WithheldSample `json:"samples"`
}

// WithheldSample is one withheld strategy: which one, at what scope, and the
// field the refusal was about when it was about a field.
type WithheldSample struct {
	StrategyID string `json:"strategy_id"`
	// Scope is STRATEGY or LEVEL; LevelID says which level when the latter.
	Scope     string `json:"scope,omitempty"`
	LevelID   uint32 `json:"level_id,omitempty"`
	FieldPath string `json:"field_path,omitempty"`
}

// SourceSampleLimit bounds how many strategies one withheld group names. The
// count says how many there are; the sample is for finding one.
const SourceSampleLimit = 20

// Dispositions the control plane assigns, as this package reads them. The
// strings are the control plane's; they are repeated here rather than
// imported so the fleet stays a reader of published facts.
const (
	dispositionAccepted              = "ACCEPTED"
	dispositionSourceIncomplete      = "SOURCE_INCOMPLETE"
	dispositionConfigRejected        = "CONFIG_REJECTED"
	dispositionStaleConfig           = "STALE_CONFIG"
	dispositionCapabilityUnsupported = "UNSUPPORTED_PHASE2_CAPABILITY"
	// The source took the strategy out of its active set: the last good
	// Plan runs one more round under PENDING_REMOVAL, and the round after
	// that records REMOVED with no Plan. Neither is a refusal.
	dispositionPendingRemoval = "PENDING_REMOVAL"
	dispositionRemoved        = "REMOVED"
	// The item was accepted with a part of its configuration read as
	// something other than what was written, the way the platform's own
	// reader reads it -- a time range that does not parse read as the whole
	// day. Not a refusal: the Plan runs. Listed beside the refusals because
	// that is where a reader looks for what the catalog did to a strategy,
	// and the words for it have to say "wider than written", not "withheld".
	dispositionConfigNormalized = "CONFIG_NORMALIZED"
)

// isWithheld says whether a disposition kept the item from running. The
// accepted item and the normalized one both run; every other disposition is
// a strategy or item that did not become a Plan this round.
func isWithheld(disposition string) bool {
	return disposition != dispositionAccepted && disposition != dispositionConfigNormalized
}

// WithheldObject is one withheld record as the control plane hands it over.
type WithheldObject struct {
	StrategyID  string
	Scope       string
	LevelID     uint32
	Disposition string
	Reason      string
	FieldPath   string
}

// NewSourceFacts folds one round's dispositions into facts. objects is the
// partition by disposition; withheld is every object that was not accepted,
// one record each.
func NewSourceFacts(at time.Time, objects map[string]int, withheld []WithheldObject) *SourceFacts {
	facts := &SourceFacts{At: at, Objects: map[string]int{}, Withheld: []WithheldGroup{}}
	for disposition, count := range objects {
		if count == 0 {
			continue
		}
		facts.Objects[disposition] = count
		// A normalized record annotates an item that is also counted as
		// accepted; adding it here would count that item twice.
		if disposition != dispositionConfigNormalized {
			facts.Listed += count
		}
		if disposition == dispositionAccepted {
			facts.Accepted = count
		}
	}
	type key struct{ disposition, reason string }
	groups := map[key]*WithheldGroup{}
	members := map[key][]WithheldSample{}
	for _, object := range withheld {
		id := key{object.Disposition, object.Reason}
		group := groups[id]
		if group == nil {
			group = &WithheldGroup{Disposition: object.Disposition, Reason: object.Reason}
			groups[id] = group
		}
		group.Count++
		members[id] = append(members[id], WithheldSample{StrategyID: object.StrategyID, Scope: object.Scope,
			LevelID: object.LevelID, FieldPath: object.FieldPath})
	}
	for id, group := range groups {
		sample := members[id]
		sort.Slice(sample, func(i, j int) bool {
			if sample[i].StrategyID != sample[j].StrategyID {
				return sortableStrategyID(sample[i].StrategyID) < sortableStrategyID(sample[j].StrategyID)
			}
			return sample[i].LevelID < sample[j].LevelID
		})
		if len(sample) > SourceSampleLimit {
			sample = sample[:SourceSampleLimit]
		}
		group.Samples = sample
		facts.Withheld = append(facts.Withheld, *group)
	}
	sort.Slice(facts.Withheld, func(i, j int) bool {
		if facts.Withheld[i].Count != facts.Withheld[j].Count {
			return facts.Withheld[i].Count > facts.Withheld[j].Count
		}
		if facts.Withheld[i].Disposition != facts.Withheld[j].Disposition {
			return facts.Withheld[i].Disposition < facts.Withheld[j].Disposition
		}
		return facts.Withheld[i].Reason < facts.Withheld[j].Reason
	})
	return facts
}

// sortableStrategyID orders identifiers numerically where they are numbers,
// which strategy identifiers are; "9" then sorts before "81" rather than
// after it, and a reader comparing the sample against the console finds them
// in the console's order.
func sortableStrategyID(id string) string {
	const width = 20
	if len(id) >= width {
		return id
	}
	padded := make([]byte, width-len(id), width)
	for index := range padded {
		padded[index] = '0'
	}
	return string(append(padded, id...))
}

// WithheldCount is how many listed strategies the round kept out of detection
// under the given dispositions. STALE_CONFIG is not among a caller's usual
// choices: such a strategy runs its last good Plan and is withheld only from
// its own change.
func (facts *SourceFacts) WithheldCount(dispositions ...string) int {
	if facts == nil {
		return 0
	}
	total := 0
	for _, disposition := range dispositions {
		total += facts.Objects[disposition]
	}
	return total
}

// Blocked says the source lists strategies and the round accepted none of
// them: the whole source is being held at the configuration step. A source
// with some strategies withheld is a line on the first screen with its
// owner. Whether a source with all of them withheld degrades the verdict is
// decided with what the deployment executes (sourceStandingOf): running
// nothing, it detects nothing whatever the badge would otherwise say;
// running its last accepted configuration, it is a cache that cannot update
// the run, which is the configuration's standing and not the run's.
func (facts *SourceFacts) Blocked() bool {
	return facts != nil && facts.Listed > 0 && facts.Accepted == 0
}

// Groups returns the withheld groups under one disposition, in the facts'
// order.
func (facts *SourceFacts) Groups(disposition string) []WithheldGroup {
	if facts == nil {
		return nil
	}
	var groups []WithheldGroup
	for _, group := range facts.Withheld {
		if group.Disposition == disposition {
			groups = append(groups, group)
		}
	}
	return groups
}

// SourceStandingKind is what the source facts mean for this deployment, decided
// against what it executes. The facts alone cannot say: a source accepting
// nothing is a deployment detecting nothing when it also runs nothing, and a
// deployment running its last accepted configuration when it runs something.
type SourceStandingKind string

const (
	// SourceAccepting: the round accepted strategies. Some may be withheld;
	// those are on the source lines with their owners.
	SourceAccepting SourceStandingKind = "ACCEPTING"
	// SourceNothingListed: the source lists no strategies. Nothing withheld,
	// nothing to run; whether that is right is the platform's question.
	SourceNothingListed SourceStandingKind = "NOTHING_LISTED"
	// SourceUpdateUnusable: the source lists strategies, the round accepted
	// none, and the deployment is running objects -- the configuration it
	// last accepted. Detection continues; what this cache cannot do is
	// update it. It is a fact about the configuration, not about the run,
	// and it does not degrade the verdict: a cache that has not been written
	// for hours proves only that nothing new was written, not that detection
	// is unavailable. A change made at the source that fails to take effect
	// because of it would be its own line, when something can see one.
	SourceUpdateUnusable SourceStandingKind = "UPDATE_UNUSABLE"
	// SourceBlocked: the source lists strategies, the round accepted none,
	// and nothing is running. The deployment detects nothing while every
	// clock above it reads fine; this is the one reading the verdict acts on.
	SourceBlocked SourceStandingKind = "BLOCKED"
)

// SourceStandingKinds is the closed list, for the page's wording table.
var SourceStandingKinds = []SourceStandingKind{SourceAccepting, SourceNothingListed, SourceUpdateUnusable, SourceBlocked}

// SourceStanding is the source facts read against the run, with the two
// sentences the first screen shows: what is running, and what the cache is.
type SourceStanding struct {
	Kind SourceStandingKind `json:"kind"`
	// Listed and Accepted are the round's; Incomplete is how many of the
	// withheld are SOURCE_INCOMPLETE -- documents without the identity the
	// contract requires, or not there at all -- because that is the count
	// the sentence names.
	Listed     int `json:"listed"`
	Accepted   int `json:"accepted"`
	Incomplete int `json:"incomplete"`
	// Normalized is how many of the listed records were accepted with part
	// of their configuration read as something other than what was written.
	// They are listed and not accepted-as-written, so a count of withheld
	// taken as Listed minus Accepted swallows them: the sentence said "1
	// withheld" about a strategy that is detecting, on the same screen as
	// the line that says it is detecting more than it asked for.
	Normalized int `json:"normalized,omitempty"`
	// Executing is how many objects the deployment runs: the catalogue's
	// count when it is known, or what the replicas own, whichever is more.
	Executing int `json:"executing"`
	// WriterAgeSeconds is how long since the source's writer moved its
	// change marker, when it has one. Evidence of when something was last
	// written, and only that: it is carried beside the verdict, not into it.
	WriterAgeSeconds *int64 `json:"writer_age_seconds,omitempty"`
	// Run is the sentence about detection, Cache the sentence about the
	// configuration source. Composed here so the page and any other reader
	// say the same thing.
	Run   string `json:"run"`
	Cache string `json:"cache"`
}

// sourceStandingOf decides the standing from the newest round and what the
// deployment executes.
func sourceStandingOf(source *SourceFacts, executing int) *SourceStanding {
	if source == nil {
		return nil
	}
	standing := &SourceStanding{Listed: source.Listed, Accepted: source.Accepted,
		Incomplete: source.WithheldCount(dispositionSourceIncomplete),
		Normalized: source.WithheldCount(dispositionConfigNormalized), Executing: executing,
		WriterAgeSeconds: source.ChangeSignalAgeSeconds}
	switch {
	case source.Listed == 0:
		standing.Kind = SourceNothingListed
	case source.Accepted > 0:
		standing.Kind = SourceAccepting
	case executing > 0:
		standing.Kind = SourceUpdateUnusable
	default:
		standing.Kind = SourceBlocked
	}
	standing.Run, standing.Cache = sourceStandingLines(standing)
	return standing
}

// sourceStandingLines composes the two sentences. The cache sentence names
// the count and what is wrong with it in the reader's words, and says what
// the cache cannot do -- not what detection is doing, which is the run
// sentence's and is decided from the run.
func sourceStandingLines(standing *SourceStanding) (run, cache string) {
	switch standing.Kind {
	case SourceNothingListed:
		return fmt.Sprintf("%d 个对象正在检测", standing.Executing), "策略缓存里没有列出任何策略"
	case SourceAccepting:
		cache = fmt.Sprintf("策略缓存列出 %d 条，可用 %d 条", standing.Listed, standing.Accepted)
		// A normalized record annotates an accepted item and is not in the
		// listed count, so the refusals are what listed minus accepted
		// leaves. The normalized ones are among the accepted and say so on
		// their own clause: the Plan runs, read otherwise than written.
		if withheld := standing.Listed - standing.Accepted; withheld > 0 {
			cache += fmt.Sprintf("，扣住 %d 条（原因见检查项）", withheld)
		}
		if standing.Normalized > 0 {
			cache += fmt.Sprintf("，可用的里有 %d 条的读法和配置写的不同（在检测，不是被扣，原因见检查项）", standing.Normalized)
		}
		return fmt.Sprintf("%d 个对象正在检测", standing.Executing), cache
	case SourceUpdateUnusable:
		return fmt.Sprintf("%d 个对象按已生效的配置继续检测", standing.Executing),
			fmt.Sprintf("当前缓存有 %d 条%s，不能用于更新配置%s", standing.Listed, sourceUnusableWord(standing), writerAgeWords(standing.WriterAgeSeconds))
	default:
		return "没有任何策略在检测",
			fmt.Sprintf("策略缓存列出 %d 条%s，一条都不能用%s", standing.Listed, sourceUnusableWord(standing), writerAgeWords(standing.WriterAgeSeconds))
	}
}

// sourceUnusableWord says what is wrong with the listed strategies when none
// was accepted, as a phrase that follows the count. All of them incomplete is
// the one shape a reader has met and is named outright; a mixture counts the
// incomplete and the rest apart, because the rest are on other lines with
// other owners; none incomplete sends the reader to those lines.
func sourceUnusableWord(standing *SourceStanding) string {
	switch {
	case standing.Incomplete == standing.Listed:
		return "身份不完整"
	case standing.Incomplete > 0:
		return fmt.Sprintf("，其中 %d 条身份不完整、%d 条因别的原因被扣（原因见检查项）", standing.Incomplete,
			standing.Listed-standing.Incomplete)
	default:
		return "，全部因别的原因被扣（原因见检查项）"
	}
}

// writerAgeWords is the writer's marker as evidence and nothing more: how long
// since anything was written, which does not by itself say what is running.
func writerAgeWords(age *int64) string {
	if age == nil {
		return ""
	}
	seconds := *age
	switch {
	case seconds >= 2*3600:
		return fmt.Sprintf("；缓存最近一次写入在 %.1f 小时前（只说明没有新写入）", float64(seconds)/3600)
	case seconds >= 120:
		return fmt.Sprintf("；缓存最近一次写入在 %d 分钟前", seconds/60)
	default:
		return fmt.Sprintf("；缓存最近一次写入在 %d 秒前", seconds)
	}
}
