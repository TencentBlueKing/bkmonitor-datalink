// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package legacyoutput emits the existing Python monitor-event protocol using
// Go only. It never invokes Python or reevaluates detection rules.
package legacyoutput

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type Event struct {
	EventID   string
	Payload   json.RawMessage
	DedupeMD5 string
}
type Snapshot struct {
	StrategyID int64
	Key        string
	Value      json.RawMessage
}
type SnapshotStore interface {
	SaveBatch(context.Context, []Snapshot) error
}
type Converter struct {
	Store          SnapshotStore
	SnapshotPrefix string
	Now            func() time.Time
	Pods           PodResolver
	PluginID       string
}

type strategyConfig struct {
	ID         int64  `json:"id"`
	BusinessID int64  `json:"bk_biz_id"`
	TenantID   string `json:"bk_tenant_id"`
	UpdateTime int64  `json:"update_time"`
	Name       string `json:"name"`
	Scenario   string `json:"scenario"`
	Items      []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Queries []struct {
			MetricID string `json:"metric_id"`
			PromQL   string `json:"promql"`
			DataType string `json:"data_type_label"`
		} `json:"query_configs"`
	} `json:"items"`
}
type preparedStrategy struct {
	config   strategyConfig
	raw      json.RawMessage
	snapshot string
	metrics  []string
	target   *PreparedTarget
}

func (c *Converter) ConvertBatch(ctx context.Context, events []contract.TriggerEventV1) ([]Event, error) {
	if len(events) == 0 {
		return []Event{}, nil
	}
	if c.Store == nil {
		return nil, fmt.Errorf("legacy snapshot store required")
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	configs := map[string]preparedStrategy{}
	snapshots := make([]Snapshot, 0)
	output := make([]Event, 0, len(events))
	pods := NewBatchPodResolver(c.Pods)
	for _, event := range events {
		if event.StrategyRef != nil || event.LegacyOutput == nil || event.LegacyOutput.Configuration == nil {
			return nil, fmt.Errorf("legacy event requires frozen context and no strategy_ref")
		}
		metadata := event.LegacyOutput.Configuration
		frozen, exists := configs[metadata.StrategyKey()]
		if !exists {
			frozen.raw = metadata.StrategyJSON()
			if err := json.Unmarshal(frozen.raw, &frozen.config); err != nil {
				return nil, err
			}
			var err error
			frozen.target, err = PrepareTarget(frozen.raw)
			if err != nil {
				return nil, err
			}
			s := frozen.config
			if s.ID <= 0 || s.BusinessID == 0 || s.UpdateTime <= 0 || s.Name == "" || len(s.Items) == 0 || len(s.Items[0].Queries) == 0 {
				return nil, fmt.Errorf("incomplete frozen legacy strategy")
			}
			frozen.snapshot = strings.TrimSuffix(c.SnapshotPrefix, ".") + ".cache.strategy.snapshot." + strconv.FormatInt(s.ID, 10) + "." + strconv.FormatInt(s.UpdateTime, 10)
			if c.SnapshotPrefix == "" {
				frozen.snapshot = strings.TrimPrefix(frozen.snapshot, ".")
			}
			seen := map[string]bool{}
			addMetric := func(value string) {
				if !seen[value] {
					seen[value] = true
					frozen.metrics = append(frozen.metrics, value)
				}
			}
			for _, item := range s.Items {
				for _, query := range item.Queries {
					addMetric(query.MetricID)
				}
			}
			for _, item := range s.Items {
				addMetric(item.Name)
			}
			for _, item := range s.Items {
				for _, query := range item.Queries {
					if query.PromQL != "" {
						addMetric(query.PromQL)
					}
				}
			}
			configs[metadata.StrategyKey()] = frozen
			snapshots = append(snapshots, Snapshot{StrategyID: s.ID, Key: frozen.snapshot, Value: frozen.raw})
		}
		converted, err := convertEvent(ctx, event, frozen, now.Unix(), pods, c.PluginID)
		if err != nil {
			return nil, err
		}
		output = append(output, converted)
	}
	// Validate every event before one bounded, de-duplicated snapshot write.
	if err := c.Store.SaveBatch(ctx, snapshots); err != nil {
		return nil, &SnapshotStoreError{Err: fmt.Errorf("save legacy snapshots: %w", err)}
	}
	return output, nil
}

// SnapshotStoreError is the snapshot store not taking the batch. It is the
// one failure ConvertBatch returns that says nothing about the events: every
// other error is the converter's own answer about their content, which it
// gives again on every retry, while this one is a store that may answer next
// time. The pod cache does not fail this way -- an unreachable cache takes
// Python's no-instance branch and converts -- so the store is the only
// dependency on this path. It marks RetryableOutputDependency so the sink and
// the coordinator treat it as they treat a broker that did not acknowledge.
type SnapshotStoreError struct {
	Err error
}

func (err *SnapshotStoreError) Error() string {
	if err == nil || err.Err == nil {
		return "legacy snapshot store failed"
	}
	return err.Err.Error()
}

func (err *SnapshotStoreError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err *SnapshotStoreError) RetryableOutputDependency() {}

func convertEvent(ctx context.Context, event contract.TriggerEventV1, frozen preparedStrategy, now int64, pods PodResolver, pluginID string) (Event, error) {
	if pluginID == "" {
		pluginID = "bkmonitor"
	}
	s := frozen.config
	metadata := event.LegacyOutput.Configuration
	if strconv.FormatInt(s.ID, 10) != event.PlanRef.StrategyID || strconv.FormatInt(s.BusinessID, 10) != event.BusinessID || (s.TenantID != "" && s.TenantID != event.TenantID) {
		return Event{}, fmt.Errorf("frozen legacy strategy identity mismatch")
	}
	itemName := ""
	for _, item := range s.Items {
		if strconv.FormatInt(item.ID, 10) == metadata.ItemID() {
			itemName = item.Name
			break
		}
	}
	if itemName == "" || event.PrimaryLevelID < 1 || event.PrimaryLevelID > 3 {
		return Event{}, fmt.Errorf("invalid legacy item/severity")
	}
	// The Python protocol represents anomaly points only. Anything else
	// reaching the converter is a routing mistake, and a loud one is better
	// than a topic filled with events Python would never have produced.
	if event.EventKind != contract.TriggerEventAbnormal {
		return Event{}, fmt.Errorf("legacy protocol carries anomaly points only, got %q", event.EventKind)
	}
	times := event.LegacyOutput.AnomalyTimestamps
	if len(times) == 0 {
		return Event{}, fmt.Errorf("ABNORMAL needs actual anomaly timestamps")
	}
	for i, ts := range times {
		if ts < 0 || ts > event.RecordRef.SourceTime || (i > 0 && ts <= times[i-1]) {
			return Event{}, fmt.Errorf("invalid actual anomaly timestamps")
		}
	}
	dimensionFields := metadata.DimensionFields()
	if metadata.DynamicDimensions() {
		for field := range event.RecordRef.Dimensions {
			dimensionFields = append(dimensionFields, field)
		}
		sort.Strings(dimensionFields)
	}
	// A no-data point's identity is its group's dimensions plus the tag, which
	// is what makes it a different object from the threshold anomaly on the
	// same series -- on both sides. The tag is on the record already; it was
	// the field list that did not have it, so the dimensions md5 was hashed
	// from the item's identity fields alone and matched nothing the backend
	// ever wrote. Nothing downstream would have called that an error: every
	// round would simply have looked like a fresh anomaly that never closes.
	//
	// The same list decides dimension_fields on the wire, so both follow from
	// the one correction.
	_, noData := event.RecordRef.Dimensions[contract.NoDataDimensionTag]
	if noData && !containsField(dimensionFields, contract.NoDataDimensionTag) {
		dimensionFields = append(append([]string(nil), dimensionFields...), contract.NoDataDimensionTag)
		sort.Strings(dimensionFields)
	}
	identity := map[string]json.RawMessage{}
	for _, field := range dimensionFields {
		value, ok := event.RecordRef.Dimensions[field]
		if !ok {
			value = json.RawMessage("null")
		}
		identity[field] = value
	}
	rawIdentity, _ := json.Marshal(identity)
	md5, err := contract.PythonValueMD5(rawIdentity)
	if err != nil {
		return Event{}, err
	}
	level := strconv.FormatUint(uint64(event.PrimaryLevelID), 10)
	anomalyID := func(ts int64) string {
		return md5 + "." + strconv.FormatInt(ts, 10) + "." + strconv.FormatInt(s.ID, 10) + "." + metadata.ItemID() + "." + level
	}
	anomalyIDs := make([]string, 0, len(times))
	for _, ts := range times {
		anomalyIDs = append(anomalyIDs, anomalyID(ts))
	}
	value := event.Observed.Values["value"]
	var description string
	if noData {
		// A no-data point has no observed value, and the backend writes none:
		// its NO_DATA_VALUE is None. Reading one here is what made every
		// synthetic record fail conversion -- the point carries its detected
		// value under its own name, and taking that for the observed value
		// would put a 1 on an alert that says there is no data.
		value = nil
		description = noDataMessage(itemName, event.Observed.Values[contract.NoDataPeriodFactField])
	} else {
		valueText, err := contract.PythonScalarText(value)
		if err != nil {
			return Event{}, err
		}
		description = fmt.Sprintf("alarmd %s: %s / %s, level=%s, value=%s", event.EventKind, s.Name, itemName, level, valueText)
	}
	projection, err := frozen.target.Project(ctx, TargetScope{TenantID: event.TenantID, BusinessID: s.BusinessID}, event.RecordRef.Dimensions, dimensionFields, pods)
	if err != nil {
		return Event{}, err
	}
	keys := make([]string, 0, len(projection.Dimensions))
	for key := range projection.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tags := make([]map[string]any, 0)
	dedupeKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		tags = append(tags, map[string]any{"key": key, "value": projection.Dimensions[key]})
		dedupeKeys = append(dedupeKeys, "tags."+key)
	}
	extraKeys := make([]string, 0, len(projection.AdditionalDimensions))
	for key := range projection.AdditionalDimensions {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		tags = append(tags, map[string]any{"key": key, "value": projection.AdditionalDimensions[key]})
	}
	additional := projection.AdditionalDimensions
	if additional == nil {
		additional = map[string]json.RawMessage{}
	}
	// Python's adapter stamps ABNORMAL on every event it produces.
	status, anomalyTime := contract.TriggerEventAbnormal, times[0]
	name := s.Name
	if _, ok := projection.Dimensions["__NO_DATA_DIMENSION__"]; ok {
		name = "[无数据] " + name
	}
	dedupe, err := contract.MonitorDedupeMD5(event.PlanRef.StrategyID, event.BusinessID, event.RecordRef.Dimensions, contract.MonitorOutputIdentity{DimensionFields: dimensionFields})
	if err != nil {
		return Event{}, err
	}
	observed := event.Observed.Values
	if noData {
		// The shape the backend writes for a point that does not exist: the
		// period it was checked for, and a null where the value would be. The
		// key is "loads" in the backend and is kept as it is -- a reader
		// comparing the two protocols matches on the name, and a better name
		// here would be a difference to explain rather than one to find.
		observed = map[string]json.RawMessage{
			"timestamp": json.RawMessage(strconv.FormatInt(event.RecordRef.SourceTime, 10)),
			"loads":     json.RawMessage("null"),
		}
	}
	data := map[string]any{"record_id": md5 + "." + strconv.FormatInt(event.RecordRef.SourceTime, 10), "time": event.RecordRef.SourceTime, "dimensions": event.RecordRef.Dimensions, "dimension_fields": dimensionFields, "value": value, "values": observed}
	payload := map[string]any{
		"event_id": anomalyID(event.RecordRef.SourceTime), "plugin_id": pluginID, "strategy_id": s.ID, "alert_name": name, "description": description, "severity": event.PrimaryLevelID, "tags": tags, "target_type": projection.Type, "target": projection.Target, "status": status, "metric": frozen.metrics, "category": s.Scenario, "data_type": s.Items[0].Queries[0].DataType, "dedupe_keys": dedupeKeys, "time": event.RecordRef.SourceTime, "anomaly_time": anomalyTime, "bk_ingest_time": now, "bk_clean_time": now, "bk_biz_id": s.BusinessID, "bk_tenant_id": event.TenantID,
		"extra_info": map[string]any{"additional_dimensions": additional, "origin_alarm": map[string]any{"trigger_time": now, "data": data, "trigger": map[string]any{"level": level, "anomaly_ids": anomalyIDs}, "anomaly": map[string]any{level: map[string]any{"anomaly_id": anomalyID(event.RecordRef.SourceTime), "anomaly_message": description}}, "dimension_translation": map[string]any{}, "strategy_snapshot_key": frozen.snapshot, "alarmd_event_id": event.EventID}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{EventID: event.EventID, Payload: raw, DedupeMD5: dedupe}, nil
}

func containsField(fields []string, name string) bool {
	for _, field := range fields {
		if field == name {
			return true
		}
	}
	return false
}

// noDataMessage is the alert text for a group that has stopped reporting.
//
// The wording is the backend's, so the two protocols say the same thing about
// the same silence. What is deliberately not said is the backend's second
// clause, "and the data is N periods late": there it is derived from the last
// data point's own timestamp against the round that noticed, and here a Slot is
// evaluated only after its readiness, so a point that arrived inside that
// window is not late. The wait alarmd already did is exactly the lateness the
// backend would be reporting, and printing it would name a condition this
// system does not have.
func noDataMessage(itemName string, periods json.RawMessage) string {
	count := int64(1)
	if len(periods) != 0 {
		if parsed, err := strconv.ParseInt(string(periods), 10, 64); err == nil && parsed > 0 {
			count = parsed
		}
	}
	return fmt.Sprintf("当前指标(%s)已经有%d个周期无数据上报", itemName, count)
}
