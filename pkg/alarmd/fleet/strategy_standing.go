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
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// One strategy's standing, asked by its id: is it detecting, and if not,
// why not -- answered from the Leader's memory of the catalog it last
// published, joined with what the fleet sees of the objects that run it.
//
// It exists because the question "why is my strategy not alerting" had no
// bounded answer on a small deployment: the strategy directory that could
// answer it is a memory projection reserved out of the resource quota, and
// a deployment too small to reserve it answered 503 -- for the five
// strategies whose blocking was the question. This reads no Redis, copies
// no cache and runs nothing in the background: the Leader indexes the
// catalog it built anyway, once per round, and a follower forwards the one
// request to the Leader.

// StrategyLookupFacts is what the process's catalog memory says about one
// strategy, in the fleet's own terms; the runtime maps the control plane's
// answer onto it.
type StrategyLookupFacts struct {
	// Available says this process holds a publication to answer from. A
	// follower, or a Leader before its first round, does not; the request
	// is then forwarded to the Leader, or refused with why there is none.
	Available bool
	// Publication identifies the catalog answered from.
	Publication StrategyPublication
	// Found says the publication records the strategy at all -- a Plan or a
	// disposition -- which tells "the source never listed it" apart from
	// "listed and withheld".
	Found bool
	// Retained says the Plans are the last good ones kept under a refusal:
	// the strategy detects on its previous configuration while the new one
	// is withheld, and both facts are on the answer.
	Retained bool
	// Plans is where the strategy runs, one per Query Group, sorted.
	Plans []StrategyPlanRef
	// Dispositions is every disposition the round recorded for the
	// strategy, accepted and withheld, one per item.
	Dispositions []StrategyDisposition
}

// StrategyPublication is the catalog a standing was answered from.
type StrategyPublication struct {
	SnapshotRevision string `json:"snapshot_revision"`
	Epoch            uint64 `json:"epoch"`
}

// StrategyPlanRef is one Plan of the strategy: which object runs it, under
// which frozen revisions, and the digest the object's content is read by.
type StrategyPlanRef struct {
	Tenant           string `json:"tenant"`
	Business         string `json:"business"`
	QueryGroup       string `json:"query_group"`
	ObjectDigest     string `json:"object_digest,omitempty"`
	SnapshotRevision string `json:"snapshot_revision"`
	QueryRevision    string `json:"query_revision"`
	ScheduleRevision string `json:"schedule_revision"`
}

// StrategyDisposition is what the round decided about one item of the
// strategy: ACCEPTED, or a disposition with the reason and the field it
// refused on. The same words the first screen's source facts count by.
type StrategyDisposition struct {
	Scope       string `json:"scope,omitempty"`
	LevelID     uint32 `json:"level_id,omitempty"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason,omitempty"`
	FieldPath   string `json:"field_path,omitempty"`
}

// StrategyLookupFunc answers the standing of one strategy from the
// process's catalog memory.
type StrategyLookupFunc func(strategyID string) StrategyLookupFacts

// CatalogAbsenceFacts is the control plane's state read live at the moment a
// point read found no catalog to answer from -- the raw facts, not a
// verdict: this package decides the word, so one place decides it and a test
// can put every state in front of it.
//
// It exists because the refusal used to name two causes from a nil pointer.
// A deployment whose Leader had failed every refresh round for half an hour
// answered "not the Leader, or the Leader before its first round" to every
// id asked for, while the same process's first screen carried the exit those
// rounds were stopping at. The reader was handed a guess, and both halves of
// it were false.
type CatalogAbsenceFacts struct {
	// Known says a refresh round has reported its outcome to this process.
	// Before the first one there is a role and nothing else.
	Known bool
	// Role is the control plane's own word: leader, follower or unacquired.
	Role string
	// Exit is where the latest failed round stopped, empty when the latest
	// round did not fail, and Text is what it said.
	Exit string
	Text string
	// FailingSeconds is how long this process has been failing its rounds,
	// absent when it is not. A count of rounds would read better and is not
	// recorded anywhere; the age is what exists, and deriving the count from
	// it would be arithmetic presented as a measurement.
	FailingSeconds *float64
	// DirectoryMounted says this process also serves the strategy directory,
	// which answers the same question from the store rather than from the
	// published catalog. It is the way out while the catalog is absent, and
	// it is only offered when the route is actually mounted here.
	DirectoryMounted bool
}

// CatalogAbsenceFunc reads that state. Nil leaves the refusal saying it does
// not know, which is the truth for a deployment that did not wire it.
type CatalogAbsenceFunc func() CatalogAbsenceFacts

// CatalogAbsenceReason is the one word for why a point read has no catalog.
// Closed.
type CatalogAbsenceReason string

const (
	// CatalogAbsenceNotLeader: this replica does not lead, and the request
	// could not be handed to the one that does.
	CatalogAbsenceNotLeader CatalogAbsenceReason = "NOT_LEADER"
	// CatalogAbsenceFirstRoundPending: this replica leads and no refresh
	// round has reported yet.
	CatalogAbsenceFirstRoundPending CatalogAbsenceReason = "FIRST_ROUND_PENDING"
	// CatalogAbsencePublishFailing: this replica leads and its refresh rounds
	// are failing, with the exit they stop at.
	CatalogAbsencePublishFailing CatalogAbsenceReason = "PUBLISH_FAILING"
	// CatalogAbsenceIndexMissing: this replica leads, its latest round did
	// not fail, and it still holds no index. Nothing is known to be wrong, so
	// none of the words above is true -- and folding this into one of them
	// would report a failure that is not happening. A rising count here is a
	// defect in the publish path, not a deployment state.
	CatalogAbsenceIndexMissing CatalogAbsenceReason = "INDEX_MISSING"
	// CatalogAbsenceUnknown: the control plane's state could not be read.
	CatalogAbsenceUnknown CatalogAbsenceReason = "UNKNOWN"
)

// CatalogAbsenceReasons is the closed list, for the page's wording table.
var CatalogAbsenceReasons = []CatalogAbsenceReason{CatalogAbsenceNotLeader, CatalogAbsenceFirstRoundPending,
	CatalogAbsencePublishFailing, CatalogAbsenceIndexMissing, CatalogAbsenceUnknown}

// CatalogAbsence is the refusal a point read returns when there is no
// catalog: the word, the sentence, and the facts the word was decided from,
// so a reader that does not know the word still learns what happened.
type CatalogAbsence struct {
	Error  string               `json:"error"`
	Reason CatalogAbsenceReason `json:"reason"`
	// Detail is the sentence, composed here so the page, the CLI and a
	// person reading the raw body are told the same thing.
	Detail         string   `json:"detail"`
	Replica        string   `json:"replica,omitempty"`
	Role           string   `json:"role,omitempty"`
	Exit           string   `json:"exit,omitempty"`
	Text           string   `json:"text,omitempty"`
	FailingSeconds *float64 `json:"failing_seconds,omitempty"`
	// Next names the route that answers the same question while this one
	// cannot, and is absent when this process does not serve it. Absent is
	// the honest answer: a way out that is not mounted is not a way out.
	Next string `json:"next,omitempty"`
}

// strategyDirectoryRoute is where the same question is answered from the
// store rather than from the published catalog.
const strategyDirectoryRoute = "/api/objects?scope=strategies"

// controlRoleLeader and controlExitNone are the control plane's words as
// they arrive here. Spelled out rather than imported: this package does not
// depend on the control plane, and the test that pins the spelling against
// the control plane's own constants is what keeps the two in step.
const (
	controlRoleLeader = "leader"
	controlExitNone   = "none"
)

// catalogAbsenceOf decides the word and writes the sentence.
//
// The order is the order the states exclude each other in: a replica that
// does not lead has nothing else to say, a leader with a failing round says
// which exit, a leader with no round yet says so, and a leader whose rounds
// are fine and still has no index is the state none of the words covers --
// which gets its own word rather than the nearest one.
func catalogAbsenceOf(facts CatalogAbsenceFacts, replica string, wired bool) CatalogAbsence {
	absence := CatalogAbsence{Error: "NOT_PUBLISHED", Replica: replica, Role: facts.Role}
	if facts.DirectoryMounted {
		absence.Next = strategyDirectoryRoute
	}
	switch {
	case !wired || facts.Role == "":
		absence.Reason, absence.Role = CatalogAbsenceUnknown, ""
		absence.Detail = "这个副本手上没有已发布的目录，也读不到控制面此刻的状态，说不出是哪一种。"
	case facts.Role != controlRoleLeader:
		absence.Reason = CatalogAbsenceNotLeader
		absence.Detail = "这个副本不是 leader（" + facts.Role + "），只有 leader 手上有已发布的目录；转发给 leader 也没成功。"
	case facts.Exit != "" && facts.Exit != controlExitNone:
		absence.Reason, absence.Exit, absence.Text = CatalogAbsencePublishFailing, facts.Exit, facts.Text
		absence.FailingSeconds = facts.FailingSeconds
		absence.Detail = "这个副本是 leader，但它的目录刷新每轮都停在 " + facts.Exit + "，一次都没发布成功" +
			failingForClause(facts.FailingSeconds) + "。策略还在按上一份能跑的配置检测，只是按 ID 查不到。"
	case !facts.Known:
		absence.Reason = CatalogAbsenceFirstRoundPending
		absence.Detail = "这个副本刚成为 leader，第一轮目录刷新还没出结果。"
	default:
		absence.Reason = CatalogAbsenceIndexMissing
		absence.Detail = "这个副本是 leader，最近一轮目录刷新没有失败，手上却没有目录——这是程序缺陷，不是部署状态。"
	}
	if absence.Next != "" {
		absence.Detail += "同一个问题可以用 " + strategyDirectoryRoute + " 从存储直接查。"
	}
	return absence
}

// failingForClause is the "for so long" half of the sentence, and is empty
// when the age is not known or under a minute rather than reading as zero.
func failingForClause(seconds *float64) string {
	if seconds == nil || *seconds < 60 {
		return ""
	}
	return "（已经 " + strconv.FormatInt(int64(*seconds)/60, 10) + " 分钟）"
}

// LeaderForward hands a request this process cannot answer to the Leader.
// It reports whether it did -- the response is then already written -- or,
// when there is no Leader to hand it to, the closed word for why (the view
// stream's discovery misses), which the refusal carries.
type LeaderForward func(response http.ResponseWriter, request *http.Request) (forwarded bool, refusal string)

// StrategyStandingKind is the one word for the strategy's standing. Closed.
type StrategyStandingKind string

const (
	// StandingDetecting: every item became a Plan and the Plans run.
	StandingDetecting StrategyStandingKind = "DETECTING"
	// StandingWithheld: listed by the source, and no item became a Plan.
	StandingWithheld StrategyStandingKind = "WITHHELD"
	// StandingPartlyWithheld: some items became Plans, some were withheld.
	StandingPartlyWithheld StrategyStandingKind = "PARTLY_WITHHELD"
	// StandingRetainedLastGood: the new configuration was withheld and the
	// Plans are the last good ones, still detecting on the old.
	StandingRetainedLastGood StrategyStandingKind = "RETAINED_LAST_GOOD"
	// StandingNotListed: the source does not list the strategy in the
	// publication answered from -- never did, or took it out and the Plan
	// has been withdrawn (a REMOVED disposition, which is not a refusal).
	StandingNotListed StrategyStandingKind = "NOT_LISTED"
)

// StrategyStandingKinds is the closed list, for the page's wording table.
var StrategyStandingKinds = []StrategyStandingKind{StandingDetecting, StandingWithheld, StandingPartlyWithheld, StandingRetainedLastGood, StandingNotListed}

// StrategyStanding is the answer.
type StrategyStanding struct {
	StrategyID string `json:"strategy_id"`
	// Tenant and Business are the filters the question carried, when it
	// did; a strategy id can repeat across tenants.
	Tenant   string `json:"tenant,omitempty"`
	Business string `json:"business,omitempty"`
	// AnsweredBy is the replica whose catalog memory answered: the Leader's,
	// whether the request reached it directly or was forwarded.
	AnsweredBy  string               `json:"answered_by"`
	Publication StrategyPublication  `json:"publication"`
	Standing    StrategyStandingKind `json:"standing"`
	Found       bool                 `json:"found"`
	Retained    bool                 `json:"retained"`
	// Plans is where the strategy runs, each with what the fleet sees of
	// that object now.
	Plans []StrategyPlanStanding `json:"plans"`
	// Dispositions is every item's decision, accepted and withheld.
	Dispositions []StrategyDisposition `json:"dispositions"`
	// Line is the answer in one sentence, composed here so the page and
	// any other reader say the same thing.
	Line string `json:"line"`
}

// StrategyPlanStanding is one Plan with the fleet's reading of its object:
// which replica holds it, and the row it is on, if any.
type StrategyPlanStanding struct {
	StrategyPlanRef
	// Replica is the replica that owns the object now, from the fleet's
	// snapshots; empty when no snapshot lists it, which Existence explains.
	Replica string `json:"replica,omitempty"`
	// Existence is the object against the catalog's active set: active,
	// absent, or unknown -- the same word the object page uses.
	Existence string `json:"existence"`
	// Rows is what the fleet lists the object under, every row: an object
	// under no row is healthy for the equation and appears as none.
	Rows []Anomaly `json:"rows"`
	// Config is the Plan's key configuration, redacted, read from the
	// frozen object on request (include=config) and absent otherwise. See
	// StrategyPlanConfigs for what it carries and what it refuses.
	Config *StrategyPlanConfigs `json:"config,omitempty"`
}

// StrategyStandingOf composes the answer from the lookup and the fleet's
// view. tenant and business narrow the Plans and dispositions when given.
func StrategyStandingOf(strategyID, tenant, business, replica string, facts StrategyLookupFacts, view *View, now time.Time) StrategyStanding {
	standing := StrategyStanding{StrategyID: strategyID, Tenant: tenant, Business: business, AnsweredBy: replica,
		Publication: facts.Publication, Found: facts.Found, Retained: facts.Retained,
		Plans: []StrategyPlanStanding{}, Dispositions: []StrategyDisposition{}}
	for _, plan := range facts.Plans {
		if tenant != "" && plan.Tenant != tenant || business != "" && plan.Business != business {
			continue
		}
		entry := StrategyPlanStanding{StrategyPlanRef: plan, Existence: "unknown", Rows: []Anomaly{}}
		if view != nil {
			entry.Existence = objectExistence(plan.QueryGroup, view.expectation)
			entry.Replica = view.ownerOf[plan.QueryGroup]
			walkObjectRows("", "", plan.QueryGroup, view, now, func(row Anomaly) {
				if len(entry.Rows) < MaxPageSize {
					entry.Rows = append(entry.Rows, row)
				}
			})
		}
		standing.Plans = append(standing.Plans, entry)
	}
	standing.Dispositions = append(standing.Dispositions, facts.Dispositions...)
	sort.SliceStable(standing.Dispositions, func(left, right int) bool {
		if standing.Dispositions[left].Scope != standing.Dispositions[right].Scope {
			return standing.Dispositions[left].Scope < standing.Dispositions[right].Scope
		}
		return standing.Dispositions[left].LevelID < standing.Dispositions[right].LevelID
	})
	standing.Standing = strategyStandingKindOf(standing)
	standing.Line = strategyStandingLine(standing)
	return standing
}

func strategyStandingKindOf(standing StrategyStanding) StrategyStandingKind {
	if !standing.Found {
		return StandingNotListed
	}
	withheld, removed := 0, 0
	for _, disposition := range standing.Dispositions {
		if isWithheld(disposition.Disposition) {
			withheld++
		}
		if disposition.Disposition == dispositionRemoved {
			removed++
		}
	}
	switch {
	case len(standing.Plans) == 0 && removed > 0 && removed == len(standing.Dispositions):
		// The source took it out and the Plan is gone: not listed, and
		// not a refusal -- a reader sent to "why was it withheld" would
		// look for a reason that is not there.
		return StandingNotListed
	case standing.Retained:
		return StandingRetainedLastGood
	case len(standing.Plans) == 0:
		return StandingWithheld
	case withheld > 0:
		return StandingPartlyWithheld
	default:
		return StandingDetecting
	}
}

// removedFromSource reports whether the dispositions say the source took
// the strategy out: REMOVED (the Plan is withdrawn) or PENDING_REMOVAL (one
// more round on the last good Plan).
func removedFromSource(standing StrategyStanding) (removed, pending bool) {
	for _, disposition := range standing.Dispositions {
		switch disposition.Disposition {
		case dispositionRemoved:
			removed = true
		case dispositionPendingRemoval:
			pending = true
		}
	}
	return removed, pending
}

// aboutOthers renders, for each Plan a row's evidence names other than the
// strategy asked about, that Plan's own state word -- never an action word,
// so the thing to do appears on that Plan's own card and this one only
// points at it.
func aboutOthers(row Anomaly, except string, words Words) []string {
	if row.Standing == nil {
		return nil
	}
	others := make([]string, 0, len(row.Standing.About))
	for _, ref := range row.Standing.About {
		if ref.StrategyID == except {
			continue
		}
		if own, given := standingForStrategy(row, ref); given {
			others = append(others, ref.StrategyID+"："+words.State[own.State])
		} else {
			others = append(others, ref.StrategyID)
		}
	}
	return others
}

// strategyStandingLine is the sentence: the standing, the objects and who
// holds them, and every withheld item with its reason and field.
func strategyStandingLine(standing StrategyStanding) string {
	withheld := make([]string, 0, len(standing.Dispositions))
	// Normalized items are not withheld: the Plan runs, wider than written.
	// They get their own clause after the standing, in the reason's words,
	// so a strategy read as whole-day is never reported as one held back.
	normalized := make([]string, 0, 1)
	for _, disposition := range standing.Dispositions {
		if disposition.Disposition == dispositionAccepted {
			continue
		}
		item := disposition.Disposition + "/" + disposition.Reason
		if disposition.Scope != "" {
			where := disposition.Scope
			if disposition.LevelID != 0 {
				where += fmt.Sprintf(" 级别 %d", disposition.LevelID)
			}
			item = where + "：" + item
		}
		if disposition.FieldPath != "" {
			item += "（" + disposition.FieldPath + "）"
		}
		if disposition.Disposition == dispositionConfigNormalized {
			normalized = append(normalized, item+"——"+WithheldWordsOf(disposition.Reason).What)
			continue
		}
		withheld = append(withheld, item)
	}
	words := ProductWords()
	objects := make([]string, 0, len(standing.Plans))
	for _, plan := range standing.Plans {
		object := shortObjectName(plan.QueryGroup)
		switch {
		case plan.Replica != "":
			object += "（" + shortReplicaName(plan.Replica) + " 持有"
			// The object's own words, not the check it is under: the check
			// is the coordinate and rides on the row, the sentence is read
			// by whoever asked about the strategy. Words the row says are
			// about another Plan on the same object are reported as that
			// Plan's state and nothing more -- no action word, so the thing
			// to do appears on one card only, the neighbour's; here the
			// neighbour is context, not a second place to act.
			if len(plan.Rows) > 0 && plan.Rows[0].Standing != nil {
				rowStanding := plan.Rows[0].Standing
				if own, given := standingForStrategy(plan.Rows[0], StrategyRef{StrategyID: standing.StrategyID, BusinessID: plan.Business}); given {
					object += "，" + words.State[own.State] + "·" + words.Action[own.Action]
				} else if others := aboutOthers(plan.Rows[0], standing.StrategyID, words); len(others) > 0 {
					object += "，在检测；同对象上策略 " + strings.Join(others, "、") + "（见该策略）"
				} else {
					object += "，" + words.State[rowStanding.State] + "·" + words.Action[rowStanding.Action]
				}
			} else if len(plan.Rows) > 0 {
				object += "，在 " + string(plan.Rows[0].Finding.Check) + " 行"
			}
			object += "）"
		case plan.Existence == "active":
			object += "（在活动集，暂无副本快照列出）"
		default:
			object += "（" + plan.Existence + "）"
		}
		objects = append(objects, object)
	}
	removed, pending := removedFromSource(standing)
	return strategyStandingSentence(standing, objects, withheld, removed, pending) + normalizedClause(normalized)
}

// normalizedClause is the sentence's tail for the items read wider than
// written; empty when there are none.
func normalizedClause(normalized []string) string {
	if len(normalized) == 0 {
		return ""
	}
	return fmt.Sprintf("；%d 项的读法和配置写的不同、在检测：%s", len(normalized), strings.Join(normalized, "；"))
}

// strategyStandingSentence is the standing, the objects and the withheld
// items, before any clause about normalized ones.
func strategyStandingSentence(standing StrategyStanding, objects, withheld []string, removed, pending bool) string {
	switch standing.Standing {
	case StandingNotListed:
		if removed {
			return "策略 " + standing.StrategyID + "：策略源已把它移出活动集，上一轮已撤下——不是被扣，是源里没有了"
		}
		return "策略 " + standing.StrategyID + "：这一轮的策略源没有列出它——不是被扣，是源里没有"
	case StandingWithheld:
		return fmt.Sprintf("策略 %s：未生效，%d 项全部被扣住：%s", standing.StrategyID, len(withheld), strings.Join(withheld, "；"))
	case StandingPartlyWithheld:
		return fmt.Sprintf("策略 %s：部分生效——%d 个对象在检测：%s；%d 项被扣住：%s",
			standing.StrategyID, len(objects), strings.Join(objects, "、"), len(withheld), strings.Join(withheld, "；"))
	case StandingRetainedLastGood:
		if pending {
			// Retained for a different reason: nothing was refused, the
			// source took the strategy out, and the last good Plan runs
			// one more round before it is withdrawn.
			return fmt.Sprintf("策略 %s：策略源已把它移出活动集，上一次生效的配置再检测一轮后撤下——%d 个对象：%s",
				standing.StrategyID, len(objects), strings.Join(objects, "、"))
		}
		return fmt.Sprintf("策略 %s：新配置被扣住，仍按上一次生效的配置检测——%d 个对象：%s；扣住的原因：%s",
			standing.StrategyID, len(objects), strings.Join(objects, "、"), strings.Join(withheld, "；"))
	default:
		return fmt.Sprintf("策略 %s：已生效，%d 个对象在检测：%s", standing.StrategyID, len(objects), strings.Join(objects, "、"))
	}
}

func shortObjectName(queryGroup string) string {
	if len(queryGroup) > 12 {
		return queryGroup[:12]
	}
	return queryGroup
}

// WithStrategyStanding serves GET /api/strategies/{id}[?tenant=&business=&include=config]
// in front of the fleet API. A process without a publication forwards the
// request to the Leader once; a forwarded request that lands on a process
// without one is refused rather than forwarded again. include=config adds
// each Plan's redacted configuration, one bounded object read per Plan; the
// words the parameter accepts are closed, and an unknown one is refused
// rather than ignored, so a reader cannot ask for something and get an
// answer that silently lacks it.
func WithStrategyStanding(next http.Handler, service *Service, lookup StrategyLookupFunc, forward LeaderForward,
	loader StrategyObjectLoader, absence CatalogAbsenceFunc, replica string, now func() time.Time, stallAfter time.Duration) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/strategies" {
			serveStrategyList(response, request, service, now, stallAfter)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/api/strategies/") {
			next.ServeHTTP(response, request)
			return
		}
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		strategyID := strings.TrimPrefix(request.URL.Path, "/api/strategies/")
		if strategyID == "" || strings.Contains(strategyID, "/") {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "STRATEGY_ID_REQUIRED"})
			return
		}
		if lookup == nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "LOOKUP_NOT_WIRED"})
			return
		}
		facts := lookup(strategyID)
		if !facts.Available {
			if forward != nil && request.Header.Get(forwardedHeader) == "" {
				if forwarded, refusal := forward(response, request); forwarded {
					return
				} else if refusal != "" {
					writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "LEADER_UNAVAILABLE", "reason": refusal})
					return
				}
			}
			// Why there is no catalog, read from the control plane now
			// rather than guessed from the nil the lookup returned.
			state, wired := CatalogAbsenceFacts{}, false
			if absence != nil {
				state, wired = absence(), true
			}
			writeJSON(response, http.StatusServiceUnavailable, catalogAbsenceOf(state, replica, wired))
			return
		}
		query := request.URL.Query()
		includes, unknown := includeWordsOf(query.Get("include"))
		if unknown != "" {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INCLUDE_UNKNOWN", "include": unknown, "accepted": IncludeConfig})
			return
		}
		var view *View
		if service != nil {
			current := service.View(request.Context())
			Decide(&current, now(), stallAfter)
			view = &current
		}
		standing := StrategyStandingOf(strategyID, query.Get("tenant"), query.Get("business"), replica, facts, view, now())
		if includes[IncludeConfig] {
			attachStrategyConfigs(request.Context(), &standing, loader)
		}
		writeJSON(response, http.StatusOK, standing)
	})
}

// includeWordsOf parses the include parameter: comma-separated words from
// the closed list, and the first word outside it.
func includeWordsOf(raw string) (words map[string]bool, unknown string) {
	words = map[string]bool{}
	for _, word := range strings.Split(raw, ",") {
		word = strings.TrimSpace(word)
		switch word {
		case "":
		case IncludeConfig:
			words[word] = true
		default:
			if unknown == "" {
				unknown = word
			}
		}
	}
	return words, unknown
}

// forwardedHeader marks a request a follower handed to the Leader, so it
// is answered or refused there and never handed on again.
const forwardedHeader = "X-Alarmd-Forwarded"

// ForwardedHeader is the header's name, for the runtime's forwarder.
func ForwardedHeader() string { return forwardedHeader }

// serveStrategyList is GET /api/strategies[?state=&action=&limit=]: one
// line per strategy over the fleet's view, most severe first, with the
// vocabulary the words are rendered from. The filter words are the closed
// lists' own; an unknown one is refused, not ignored, so a reader cannot
// ask for a column and get every column.
func serveStrategyList(response http.ResponseWriter, request *http.Request, service *Service, now func() time.Time, stallAfter time.Duration) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if service == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "FLEET_NOT_WIRED"})
		return
	}
	query := request.URL.Query()
	state, action := StateWord(query.Get("state")), ActionWord(query.Get("action"))
	if state != "" && !knownStateWord(state) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "STATE_UNKNOWN", "state": state, "accepted": StateWords})
		return
	}
	if action != "" && !knownActionWord(action) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "ACTION_UNKNOWN", "action": action, "accepted": ActionWords})
		return
	}
	limit := MaxStrategyLines
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "LIMIT_INVALID", "limit": raw})
			return
		}
		if parsed < limit {
			limit = parsed
		}
	}
	current := service.View(request.Context())
	Decide(&current, now(), stallAfter)
	all := StrategyLines(&current, now())
	lines := FilterStrategyLines(all, state, action)
	body := StrategyListResponse{Words: ProductWords(), Strategies: lines, Summary: SummarizeStrategyLines(all), Total: len(lines), State: state, Action: action}
	if len(lines) > limit {
		body.Strategies, body.Truncated = lines[:limit], true
	}
	body.Listed = len(body.Strategies)
	writeJSON(response, http.StatusOK, body)
}

func knownStateWord(word StateWord) bool {
	for _, known := range StateWords {
		if known == word {
			return true
		}
	}
	return false
}

func knownActionWord(word ActionWord) bool {
	for _, known := range ActionWords {
		if known == word {
			return true
		}
	}
	return false
}
