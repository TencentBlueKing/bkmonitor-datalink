package contract

import (
	"bytes"
	"crypto/md5" // The platform's comparison is MD5, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// StrategyFactsSchemaV2 names the one form of StrategyFactsV2 this build
// writes and reads.
const StrategyFactsSchemaV2 = "v2"

// StrategyFactsV2 is what an event says about the strategy configuration it
// was decided under, for a later reader to tell whether the strategy has
// changed in a way that orphans the alert the event opened: the platform
// closes an alert when its strategy's metrics or grouping dimensions
// change, and closes a no-data alert when its filter conditions change
// (bk-monitor alert manager, CloseStatusChecker.check_strategy_changed).
// The alert link copies an alert's extra_data from its opening event and
// never changes it, so the facts on an alert are those of the revision that
// raised it.
//
// The values are derived from the strategy's first item, as the platform
// reads it:
//
//   - MetricDigest is the hex MD5 of the JSON array of the query configs'
//     metric_id strings, in query order, encoded without HTML escaping. The
//     platform compares the metric ids as an ordered list, and two digests
//     are equal exactly when the lists are. A PromQL query published with
//     an empty metric_id (data_source_label prometheus, data_type_label
//     time_series) counts under the id the platform derives from its text
//     (PromQLMetricID).
//   - ConditionMD5 is the platform's get_agg_condition_md5 of the query
//     config's conditions as read for its ConditionBasis (AggConditionMD5,
//     over queryConditions) when the item has one query config. With
//     several, it is the hex MD5 of their AggConditionMD5s joined by "," in
//     query order: the platform compares the conditions query by query and
//     closes on any difference, which a digest of the ordered list states
//     the same way.
//   - DimensionDigest is the GroupingDigest of the dimensions the event's
//     series is grouped by, the key set its alert fingerprint is built from:
//     the Plan's output identity fields for a threshold event, and the
//     no-data configuration's agg_dimension for a no-data event. Empty when
//     the grouping is every dimension the series carries (dynamic
//     dimensions), which no fixed set states.
//   - ConditionBasis says what ConditionMD5 was computed over (the
//     ConditionBasis* words), the weakest across the item's queries in the
//     order snapshot_unreadable < query_injected < query < snapshot_user: a
//     digest is only as good as its least certain query.
//   - NoDataDimensionDigest is on a Plan only, beside DimensionDigest: the
//     GroupingDigest of its no-data agg_dimension, empty when the Plan
//     detects no no-data. ForEvent puts the one an event is grouped by in
//     DimensionDigest and leaves this out, so an event carries one grouping.
type StrategyFactsV2 struct {
	Schema                string `json:"schema"`
	MetricDigest          string `json:"metric_digest"`
	ConditionMD5          string `json:"condition_md5"`
	DimensionDigest       string `json:"dimension_digest,omitempty"`
	ConditionBasis        string `json:"condition_basis"`
	NoDataDimensionDigest string `json:"no_data_dimension_digest,omitempty"`
}

// The bases a ConditionMD5 is computed over, from the strongest:
const (
	// ConditionBasisSnapshotUser: every query is a catalog metric whose
	// conditions are read from its metric materialization snapshot, the
	// user's original input (queryConditions).
	ConditionBasisSnapshotUser = "snapshot_user"
	// ConditionBasisQuery: a query has no snapshot and its published
	// agg_condition holds no key the publisher injects.
	ConditionBasisQuery = "query"
	// ConditionBasisQueryInjected: a query has no snapshot and its published
	// agg_condition holds a key the publisher injects (InjectedConditionKeys),
	// so the digest moves with the publisher's own state too.
	ConditionBasisQueryInjected = "query_injected"
	// ConditionBasisSnapshotUnreadable: a query has a snapshot whose
	// conditions are missing or not a list of conditions with key, method
	// and value; the digest was computed over the published agg_condition
	// and says nothing about the user's input.
	ConditionBasisSnapshotUnreadable = "snapshot_unreadable"
)

// conditionBasisRank orders the bases from the weakest.
var conditionBasisRank = map[string]int{ConditionBasisSnapshotUnreadable: 0, ConditionBasisQueryInjected: 1,
	ConditionBasisQuery: 2, ConditionBasisSnapshotUser: 3}

// InjectedConditionKeys are the condition keys the strategy publisher adds
// to a query's agg_condition on its own, which no marker in the published
// item tells apart from a condition the user wrote (the publisher's
// strategy cache projector):
//
//   - device_type: the file system type exclusion, from the deployment's
//     dynamic file_system_type_ignore list, intersected with every OR branch
//     of a bk_monitor time_series system.disk query
//     (_with_file_system_type_ignore, :234-247, applied at :468-475);
//   - bk_biz_id: the business condition of a strategy spanning businesses,
//     ANDed into every OR branch (_business_condition and
//     _with_business_condition, :204-224, applied at :456-460).
//
// Both are added to the query's agg_condition, never to a metric
// materialization snapshot, so a query whose conditions are read from its
// snapshot (queryConditions) is not affected by them. A key here only names
// the basis (query_injected); nothing is decided from it.
var InjectedConditionKeys = []string{"device_type", "bk_biz_id"}

// PromQLMetricID is the platform's get_metric_id for a prometheus
// time_series query (bk-monitor bkmonitor/strategy/new_strategy.py:116-162):
// the PromQL text, or past 128 characters its first 125 and "...",
// characters counted as Python counts them, by code point.
func PromQLMetricID(promql string) string {
	runes := []rune(promql)
	if len(runes) > 128 {
		return string(runes[:125]) + "..."
	}
	return promql
}

// ForEvent is the facts an event of the Plan carries: its grouping is the
// no-data one for a no-data event and the Plan's own for every other.
func (facts StrategyFactsV2) ForEvent(noData bool) StrategyFactsV2 {
	if noData {
		facts.DimensionDigest = facts.NoDataDimensionDigest
	}
	facts.NoDataDimensionDigest = ""
	return facts
}

// Digest is the facts as one word: the hex MD5 of their JSON encoding, the
// bytes an event's extra_data.strategy_facts carries. An event writes it as
// labels.strategy_facts_digest beside the facts, so alerts can be selected
// by it at the link (its search indexes labels.*, never extra_data); two
// digests are equal exactly when the facts are. Taken of an event's facts
// (ForEvent), never of a Plan's, which carry both groupings.
func (facts StrategyFactsV2) Digest() string {
	encoded, _ := json.Marshal(facts)
	return md5Hex(string(encoded))
}

// StrategyGrouping is what a Plan's series are grouped by: its output
// identity fields (Fields, or every dimension when Dynamic), and its no-data
// configuration's agg_dimension when it detects no-data.
type StrategyGrouping struct {
	Fields       []string
	Dynamic      bool
	NoData       bool
	NoDataFields []string
}

// GroupingDigest is the hex MD5 of the JSON array, encoded without HTML
// escaping, of the grouping keys with a "tags." prefix removed - the
// fingerprint reads a key and its tags. form as one
// (ProjectMonitorTarget) - without repeats and sorted by byte. Two digests
// are equal exactly when the key sets are, whatever order the strategy
// lists them in, as the platform compares them (as sets).
func GroupingDigest(fields []string) string {
	seen := make(map[string]struct{}, len(fields))
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		key := strings.TrimPrefix(field, "tags.")
		if _, repeated := seen[key]; repeated {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return md5Hex(jsonText(keys))
}

// DeriveStrategyFactsV2 derives the facts from an item's query_configs and
// its Plan's grouping. A query config without a metric_id string, or whose
// agg_condition is not a list of objects, has no facts: the platform's own
// comparison cannot read it either.
func DeriveStrategyFactsV2(queryConfigs []json.RawMessage, grouping StrategyGrouping) (*StrategyFactsV2, error) {
	metricIDs := make([]string, 0, len(queryConfigs))
	conditions := make([]string, 0, len(queryConfigs))
	basis := ConditionBasisSnapshotUser
	for index, raw := range queryConfigs {
		var query struct {
			MetricID        *string         `json:"metric_id"`
			DataSourceLabel string          `json:"data_source_label"`
			DataTypeLabel   string          `json:"data_type_label"`
			PromQL          string          `json:"promql"`
			AggCondition    json.RawMessage `json:"agg_condition"`
			MetricSnapshot  json.RawMessage `json:"metric_snapshot"`
		}
		if err := json.Unmarshal(raw, &query); err != nil {
			return nil, fmt.Errorf("strategy facts: query config %d: %w", index, err)
		}
		if query.MetricID == nil {
			return nil, fmt.Errorf("strategy facts: query config %d names no metric_id", index)
		}
		metricID := *query.MetricID
		if metricID == "" && query.DataSourceLabel == "prometheus" && query.DataTypeLabel == "time_series" {
			metricID = PromQLMetricID(query.PromQL)
		}
		metricIDs = append(metricIDs, metricID)
		condition, queryBasis := queryConditions(query.AggCondition, query.MetricSnapshot)
		if conditionBasisRank[queryBasis] < conditionBasisRank[basis] {
			basis = queryBasis
		}
		if isJSONNullOrMissing(condition) {
			// The platform reads a missing agg_condition as an empty one.
			condition = json.RawMessage("[]")
		}
		digest, err := AggConditionMD5(condition)
		if err != nil {
			return nil, fmt.Errorf("strategy facts: query config %d: %w", index, err)
		}
		conditions = append(conditions, digest)
	}
	facts := &StrategyFactsV2{Schema: StrategyFactsSchemaV2, MetricDigest: md5Hex(jsonText(metricIDs)), ConditionBasis: basis}
	if len(conditions) == 1 {
		facts.ConditionMD5 = conditions[0]
	} else {
		facts.ConditionMD5 = md5Hex(strings.Join(conditions, ","))
	}
	if !grouping.Dynamic {
		facts.DimensionDigest = GroupingDigest(grouping.Fields)
	}
	if grouping.NoData {
		facts.NoDataDimensionDigest = GroupingDigest(grouping.NoDataFields)
	}
	return facts, nil
}

// queryConditions is the conditions one query's ConditionMD5 is computed
// over, and their basis.
//
// A query of a catalog metric is published (by the publisher's metric
// resolver) with the metric row's object_filter and, for an expanded member,
// the member recipe's conditions merged into its agg_condition; its
// metric_snapshot keeps the user's original input beside them, and that is
// read:
//
//   - a snapshot with a derived block is an expanded member. Its own
//     agg_condition holds the merged conditions: the snapshot is built from
//     the user's and then overwritten with the combined ones.
//     derived.agg_condition is the user's, the anchor the publisher folds
//     members back to, and that is read.
//   - any other snapshot: its agg_condition.
//
// The snapshot conditions are taken only if they are a list of conditions
// each with key, method and value; otherwise - missing, null, another shape -
// the basis is snapshot_unreadable and the published agg_condition is
// digested instead. (The publisher's own reader falls back to the published
// conditions when the key is missing; that is not taken here as the user's
// input.) A query without a snapshot is read as published, and
// its basis is query or query_injected (InjectedConditionKeys).
func queryConditions(conditions, snapshot json.RawMessage) (json.RawMessage, string) {
	if isJSONNullOrMissing(snapshot) {
		if hasInjectedKey(conditions) {
			return conditions, ConditionBasisQueryInjected
		}
		return conditions, ConditionBasisQuery
	}
	// A snapshot or derived block that is not an object reads as one
	// without conditions: unreadable.
	var source map[string]json.RawMessage
	_ = json.Unmarshal(snapshot, &source)
	if raw, member := source["derived"]; member {
		var derived map[string]json.RawMessage
		_ = json.Unmarshal(raw, &derived)
		source = derived
	}
	user, ok := source["agg_condition"]
	if !ok || !conditionList(user) {
		return conditions, ConditionBasisSnapshotUnreadable
	}
	return user, ConditionBasisSnapshotUser
}

// conditionList reports whether raw is a list of conditions, each an
// object with key, method and value.
func conditionList(raw json.RawMessage) bool {
	var list []map[string]json.RawMessage
	if json.Unmarshal(raw, &list) != nil || list == nil {
		return false
	}
	for _, condition := range list {
		for _, field := range []string{"key", "method", "value"} {
			if _, ok := condition[field]; !ok {
				return false
			}
		}
	}
	return true
}

// hasInjectedKey reports whether published conditions hold a key the
// publisher injects (InjectedConditionKeys).
func hasInjectedKey(conditions json.RawMessage) bool {
	var keyed []struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(conditions, &keyed)
	for _, condition := range keyed {
		if slices.Contains(InjectedConditionKeys, condition.Key) {
			return true
		}
	}
	return false
}

func isJSONNullOrMissing(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}

// jsonText is a string list as a JSON array, without HTML escaping.
func jsonText(values []string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(values)
	return strings.TrimSuffix(buffer.String(), "\n")
}

// AggConditionMD5 is the platform's get_agg_condition_md5 (bk-monitor
// alarm_backends/service/alert/manager/checker/close.py): of each condition
// only key, value, method and condition are kept, the first condition's
// connector is dropped - it is always AND and does not count as a change -
// and the list is digested by bkmonitor.utils.common_utils.count_md5
// (pythonCountMD5, the same implementation the alert fingerprint uses).
func AggConditionMD5(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var conditions []any
	if err := decoder.Decode(&conditions); err != nil || conditions == nil {
		return "", errors.New("agg_condition is not a list")
	}
	kept := make([]any, 0, len(conditions))
	for index, condition := range conditions {
		fields, ok := condition.(map[string]any)
		if !ok {
			return "", fmt.Errorf("agg_condition %d is not an object", index)
		}
		target := make(map[string]any, 4)
		for _, key := range []string{"key", "value", "method", "condition"} {
			if value, present := fields[key]; present {
				target[key] = value
			}
		}
		if index == 0 {
			delete(target, "condition")
		}
		kept = append(kept, target)
	}
	return pythonCountMD5(kept)
}

func md5Hex(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}
