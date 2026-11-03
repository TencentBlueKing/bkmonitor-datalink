// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The fields a filter names, as the link indexes them (b7e5beddf
// docs/reference/contracts/alert-search.md, "queryable fields"): keywords
// at the top, labels.* and dimensions.* flattened, every value a keyword,
// so a version or an id is matched as its decimal text.
const (
	fieldTenant  = "bk_tenant_id"
	fieldSource  = "event_source_id"
	fieldAlertID = "alert_id"
	fieldPrint   = "fingerprint"
	fieldStatus  = "status"
	fieldLevel   = "severity"
	// fieldCreated is when the link opened the alert (date_nanos,
	// alert-search.md "可查询字段").
	fieldCreated = "create_at"
	labelID      = "labels.strategy_id"
	labelVersion = "labels.strategy_version"
	labelDigest  = "labels.strategy_facts_digest"
	// dimensionNoData is the no-data tag every no-data alert carries
	// (contract.NoDataDimensionTag) and no threshold alert does: it tells
	// the evaluation family without a label.
	dimensionNoData = "dimensions." + contract.NoDataDimensionTag
)

// Scope is what every filter alarmd sends is under: the tenant, the
// deployment's own event source and one strategy. The link adds only the
// tenant and status=active (b7e5beddf internal/controlplane/api/
// close_filter.go:84-86); the source and the strategy are alarmd's to add,
// and a filter without them would select another source's alerts or
// another strategy's.
type Scope struct {
	TenantID      string
	EventSourceID string
	StrategyID    string
}

func (scope Scope) validate() error {
	if scope.TenantID == "" || scope.EventSourceID == "" {
		return errors.New("alarmd filterclose: a filter's tenant and event source are required")
	}
	if id, err := strconv.ParseInt(scope.StrategyID, 10, 64); err != nil || id <= 0 || strconv.FormatInt(id, 10) != scope.StrategyID {
		return errors.New("alarmd filterclose: a filter's strategy is a positive decimal id")
	}
	return nil
}

// Filter is an Elasticsearch query object made by this file's builders,
// and only by them: each puts the scope's three terms first in the top
// bool.filter, and requireScope checks they are there before anything is
// sent.
type Filter struct {
	scope Scope
	body  json.RawMessage
	// batch names the series of a per-series filter, in the decision's
	// key: another batch of one decision is another command, with its own
	// effective_at.
	batch string
}

// batchOf is a digest of what a per-series filter names.
func batchOf(prefix string, encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return prefix + hex.EncodeToString(sum[:16])
}

// Scope is the filter's scope.
func (filter Filter) Scope() Scope { return filter.scope }

// JSON is the query object.
func (filter Filter) JSON() json.RawMessage { return filter.body }

type object = map[string]any

func term(field, value string) object { return object{"term": object{field: value}} }

func terms(field string, values []string) object { return object{"terms": object{field: values}} }

func exists(field string) object { return object{"exists": object{"field": field}} }

// openedSince is filter narrowed to the alerts the link opened at or after
// since: the ones alarmd's record vouches for (openepisodes.SinceField).
func openedSince(filter Filter, since time.Time) (Filter, error) {
	return narrowed(filter, object{"range": object{fieldCreated: object{"gte": since.UTC().Format(time.RFC3339Nano)}}})
}

// openedAfter is filter narrowed to the alerts the link opened after at,
// on the link's own clock.
func openedAfter(filter Filter, at time.Time) (Filter, error) {
	return narrowed(filter, object{"range": object{fieldCreated: object{"gt": at.UTC().Format(time.RFC3339Nano)}}})
}

// openedBefore is filter narrowed to the alerts the link opened before
// since: the ones the record cannot vouch for, decided alert by alert.
func openedBefore(filter Filter, since time.Time) (Filter, error) {
	return narrowed(filter, object{"range": object{fieldCreated: object{"lt": since.UTC().Format(time.RFC3339Nano)}}})
}

// openedBy is the alerts the link opened at or before at: a decision read
// at that moment is not about an alert opened after it.
//
// at is on alarmd's clock and create_at on the link's, and nothing here
// corrects for the two differing. A link whose clock runs behind alarmd's
// by d stamps an alert opened up to d after the decision at or before at,
// and the close takes it although the decision was not about it: skew
// narrows the window this bound protects by d. A link whose clock runs
// ahead leaves an alert opened up to d before the decision out of the
// close, open for a later decision. The bound holds only as well as the
// two hosts' clocks agree.
func openedBy(at time.Time) (object, error) {
	if at.IsZero() {
		return nil, errors.New("alarmd filterclose: a per-series close names the moment its decision was read")
	}
	return object{"range": object{fieldCreated: object{"lte": at.UTC().Format(time.RFC3339Nano)}}}, nil
}

// build is the one builder: the scope's terms, then the clauses.
func build(scope Scope, filter, mustNot []any) (Filter, error) {
	if err := scope.validate(); err != nil {
		return Filter{}, err
	}
	clauses := append([]any{term(fieldTenant, scope.TenantID), term(fieldSource, scope.EventSourceID), term(labelID, scope.StrategyID)}, filter...)
	query := object{"filter": clauses}
	if len(mustNot) > 0 {
		query["must_not"] = mustNot
	}
	body, err := json.Marshal(object{"bool": query})
	if err != nil {
		return Filter{}, err
	}
	built := Filter{scope: scope, body: body}
	if err := requireScope(built); err != nil {
		return Filter{}, err
	}
	return built, nil
}

// errUnscoped is a filter whose top bool.filter does not name its scope's
// tenant, source and strategy. Nothing is sent with one.
var errUnscoped = errors.New("alarmd filterclose: the filter does not carry its tenant, event source and strategy")

// requireScope is the check every request goes through: the top-level
// bool.filter holds a term for each of the scope's tenant, source and
// strategy, with the scope's values.
func requireScope(filter Filter) error {
	if filter.scope.validate() != nil {
		return errUnscoped
	}
	var query struct {
		Bool struct {
			Filter []map[string]map[string]json.RawMessage `json:"filter"`
		} `json:"bool"`
	}
	if json.Unmarshal(filter.body, &query) != nil {
		return errUnscoped
	}
	want := map[string]string{fieldTenant: filter.scope.TenantID, fieldSource: filter.scope.EventSourceID, labelID: filter.scope.StrategyID}
	for _, clause := range query.Bool.Filter {
		for field, raw := range clause["term"] {
			var value string
			if expected, named := want[field]; named && json.Unmarshal(raw, &value) == nil && value == expected {
				delete(want, field)
			}
		}
	}
	if len(want) > 0 {
		return errUnscoped
	}
	return nil
}

// StrategyFilter is every alert of the strategy: the absent close.
func StrategyFilter(scope Scope) (Filter, error) { return build(scope, nil, nil) }

// VersionFilter is the strategy's alerts opened at one version: an
// inactive close decided for that version only.
func VersionFilter(scope Scope, version string) (Filter, error) {
	if !decimal(version) {
		return Filter{}, errors.New("alarmd filterclose: a version is a decimal text")
	}
	return build(scope, []any{term(labelVersion, version)}, nil)
}

// Bucket is the alerts of one strategy opened at one version, with one
// facts digest, of one family. Digest empty is the alerts opened without
// a digest label (before events carried it), which are decided one by one.
type Bucket struct {
	Version string
	Digest  string
	NoData  bool
}

func (bucket Bucket) clauses() (filter, mustNot []any) {
	filter = []any{term(labelVersion, bucket.Version)}
	if bucket.Digest != "" {
		filter = append(filter, term(labelDigest, bucket.Digest))
	} else {
		mustNot = append(mustNot, exists(labelDigest))
	}
	if bucket.NoData {
		filter = append(filter, exists(dimensionNoData))
	} else {
		mustNot = append(mustNot, exists(dimensionNoData))
	}
	return filter, mustNot
}

// BucketFilter is one bucket's alerts.
func BucketFilter(scope Scope, bucket Bucket) (Filter, error) {
	if !decimal(bucket.Version) || (bucket.Digest != "" && !hexDigest(bucket.Digest)) {
		return Filter{}, errors.New("alarmd filterclose: a bucket is a decimal version and a hex digest")
	}
	filter, mustNot := bucket.clauses()
	return build(scope, filter, mustNot)
}

// alertsFilter is a bucket's alerts among ids, the per-alert decisions'
// close: still in the bucket when it runs, or not closed.
func alertsFilter(scope Scope, bucket Bucket, ids []string) (Filter, error) {
	if len(ids) == 0 {
		return Filter{}, errors.New("alarmd filterclose: no alert ids")
	}
	filter, mustNot := bucket.clauses()
	return build(scope, append(filter, terms(fieldAlertID, ids)), mustNot)
}

// FingerprintFilter is the strategy's alerts of the given fingerprints
// opened at or before decidedAt: a close decided per series (an identity
// superseded) at that moment, each fingerprint naming at most one active
// alert. A fingerprint names the series, not one opening of it: without
// the bound, an alert the series opened again after the decision would be
// selected and closed too.
func FingerprintFilter(scope Scope, fingerprints []string, decidedAt time.Time) (Filter, error) {
	if len(fingerprints) == 0 || len(fingerprints) > maxTargetClauses {
		return Filter{}, errors.New("alarmd filterclose: 1 to 500 fingerprints")
	}
	bound, err := openedBy(decidedAt)
	if err != nil {
		return Filter{}, err
	}
	built, err := build(scope, []any{terms(fieldPrint, sorted(fingerprints)), bound}, nil)
	if err != nil {
		return Filter{}, err
	}
	encoded, _ := json.Marshal(sorted(fingerprints))
	built.batch = batchOf("fingerprints:", encoded)
	return built, nil
}

// LevelFilter is the threshold alerts of the given versions at a level the
// current Plans no longer detect: level_removed. Versions are the older
// ones a round found, never the current one or one ahead of it.
func LevelFilter(scope Scope, versions, severities []string) (Filter, error) {
	if len(versions) == 0 || len(severities) == 0 {
		return Filter{}, errors.New("alarmd filterclose: level_removed needs versions and severities")
	}
	for _, version := range versions {
		if !decimal(version) {
			return Filter{}, errors.New("alarmd filterclose: a version is a decimal text")
		}
	}
	return build(scope, []any{terms(labelVersion, sorted(versions)), terms(fieldLevel, sorted(severities))}, []any{exists(dimensionNoData)})
}

// enumerationFilter is the strategy's alerts that carry a version, other
// than the current version's and the buckets already seen: limit 1 of it
// is the next bucket's sample.
func enumerationFilter(scope Scope, current string, seen []Bucket) (Filter, error) {
	mustNot := []any{term(labelVersion, current)}
	for _, bucket := range seen {
		filter, not := bucket.clauses()
		inner := object{"filter": filter}
		if len(not) > 0 {
			inner["must_not"] = not
		}
		mustNot = append(mustNot, object{"bool": inner})
	}
	return build(scope, []any{exists(labelVersion)}, mustNot)
}

// withAlertID is filter narrowed to one alert, any status: the pending
// retry's check that the alert is still one the decision named.
func withAlertID(filter Filter, alertID string) (Filter, error) {
	return narrowed(filter, term(fieldAlertID, alertID))
}

// activeOnly is filter narrowed to active alerts, for a search: the link's
// search reads its history as well (alert-search.md: "覆盖当前部署的 Active
// 与 History 查询目标"); only the filtered close adds status=active itself.
func activeOnly(filter Filter) (Filter, error) {
	return narrowed(filter, term(fieldStatus, "active"))
}

// narrowed is filter with one more clause in its top bool.filter.
func narrowed(filter Filter, clause any) (Filter, error) {
	var query map[string]map[string]json.RawMessage
	if err := json.Unmarshal(filter.body, &query); err != nil || query["bool"] == nil {
		return Filter{}, errUnscoped
	}
	var clauses []any
	if err := json.Unmarshal(query["bool"]["filter"], &clauses); err != nil {
		return Filter{}, errUnscoped
	}
	encoded, err := json.Marshal(append(clauses, clause))
	if err != nil {
		return Filter{}, err
	}
	query["bool"]["filter"] = encoded
	body, err := json.Marshal(query)
	if err != nil {
		return Filter{}, err
	}
	result := Filter{scope: filter.scope, body: body, batch: filter.batch}
	return result, requireScope(result)
}

// discoveryFilter is the tenant's active alerts of the source whose
// strategy is none of running: a read that names no one strategy, the
// absent close's discovery. Its own check is that it carries the tenant,
// the source and status active at the top.
func discoveryFilter(tenantID, eventSourceID string, running []string) (json.RawMessage, error) {
	if tenantID == "" || eventSourceID == "" {
		return nil, errUnscoped
	}
	query := object{"filter": []any{term(fieldTenant, tenantID), term(fieldSource, eventSourceID), term(fieldStatus, "active"),
		exists(labelID)}}
	if len(running) > 0 {
		query["must_not"] = []any{terms(labelID, sorted(running))}
	}
	body, err := json.Marshal(object{"bool": query})
	if err != nil {
		return nil, err
	}
	if len(body) > maxDiscoveryBodyBytes {
		return nil, errDiscoveryTooLarge
	}
	return body, nil
}

// maxDiscoveryBodyBytes keeps the discovery under the link's request bound
// (1 MiB by default, dispatch.alert_search.max_request_bytes), half of it
// left for the envelope and a bound set lower: about 50,000 strategy ids.
const maxDiscoveryBodyBytes = 512 << 10

var errDiscoveryTooLarge = errors.New("alarmd filterclose: the running strategies do not fit one discovery")

// Target is one target of a strategy's scope, by the dimensions its alerts
// carry: every key must match (an IP and its cloud area together), so
// (A, 1) does not match (A, 0).
type Target map[string]string

// The bounds of one filter's targets and fingerprints. The link embeds the filter in
// one Elasticsearch query whose leaf clauses count against the cluster's
// indices.query.bool.max_clause_count (1024 by default in 7.17, and the
// total over the query in 8.x), and the body against the link's request
// bound (1 MiB by default, dispatch.alert_search.max_request_bytes).
// Half of each is left for the scope, the command and a cluster set lower.
const (
	maxTargetClauses   = 500
	maxTargetBodyBytes = 512 << 10
)

func decimal(text string) bool {
	if text == "" || len(text) > 20 {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hexDigest(text string) bool {
	if len(text) != 32 {
		return false
	}
	for _, r := range text {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
