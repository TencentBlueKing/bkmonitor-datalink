package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// foldedDataset is one merged Dataset with its full view, shared by every
// binding of the streamed batch that folded into the same earlier Dataset.
type foldedDataset struct {
	dataset *execution.Dataset
	view    *execution.DatasetView
}

// foldStreamedDataset merges the records of a second provider row set into
// the Dataset an earlier batch streamed for the same (consumer, series,
// requirement) key, following the fold policy the compiled algorithm declares.
// The provider row grain is finer than the series identity for such plans
// (ProcPort: one row per port set of a process), so rows of one identity and
// source time fold into one record instead of failing the Slot as a duplicate.
func foldStreamedDataset(existing, incoming *execution.Dataset, policy strategy.SeriesFoldPolicy) (foldedDataset, error) {
	if existing == nil || incoming == nil {
		return foldedDataset{}, errors.New("alarmd worker: fold requires both Datasets")
	}
	records, err := foldStreamedRecords(existing.Records(), incoming.Records(), policy)
	if err != nil {
		return foldedDataset{}, err
	}
	dataset := execution.NewDataset(records)
	ordinals := make([]uint32, dataset.Len())
	for index := range ordinals {
		ordinals[index] = uint32(index)
	}
	view, err := execution.NewDatasetView(dataset, ordinals)
	if err != nil {
		return foldedDataset{}, err
	}
	return foldedDataset{dataset: dataset, view: view}, nil
}

// foldStreamedRecords merges incoming into existing. Records that share a
// source time (and therefore a record id) fold field by field under the
// declared rules; every other record is kept. The result is in source order.
func foldStreamedRecords(existing, incoming []contract.CanonicalRecordV2, policy strategy.SeriesFoldPolicy) ([]contract.CanonicalRecordV2, error) {
	merged := make([]contract.CanonicalRecordV2, 0, len(existing)+len(incoming))
	byTime := make(map[int64]int, len(existing)+len(incoming))
	for _, record := range existing {
		byTime[record.SourceTime] = len(merged)
		merged = append(merged, record)
	}
	for _, record := range incoming {
		index, collides := byTime[record.SourceTime]
		if !collides {
			byTime[record.SourceTime] = len(merged)
			merged = append(merged, record)
			continue
		}
		folded, err := foldRecordPair(merged[index], record, policy)
		if err != nil {
			return nil, err
		}
		merged[index] = folded
	}
	sort.SliceStable(merged, func(left, right int) bool { return merged[left].SourceTime < merged[right].SourceTime })
	return merged, nil
}

func foldRecordPair(existing, incoming contract.CanonicalRecordV2, policy strategy.SeriesFoldPolicy) (contract.CanonicalRecordV2, error) {
	if existing.RecordID != incoming.RecordID || existing.SourceTime != incoming.SourceTime ||
		existing.BusinessID != incoming.BusinessID || existing.DimensionIdentity.Digest != incoming.DimensionIdentity.Digest {
		return contract.CanonicalRecordV2{}, errors.New("alarmd worker: folded rows differ in record identity")
	}
	values, err := foldRawFields(existing.Values, incoming.Values, policy.Values, "value")
	if err != nil {
		return contract.CanonicalRecordV2{}, err
	}
	dimensions, err := foldRawFields(existing.Dimensions, incoming.Dimensions, policy.Dimensions, "dimension")
	if err != nil {
		return contract.CanonicalRecordV2{}, err
	}
	folded := existing
	folded.Values, folded.Dimensions = values, dimensions
	if folded.CollectionTime == nil && incoming.CollectionTime != nil {
		collected := *incoming.CollectionTime
		folded.CollectionTime = &collected
	}
	return folded, nil
}

// foldRawFields folds two field maps. A field present in one row only is
// kept; a field present in both folds under its declared rule; an undeclared
// field must carry the same value in both rows (identity dimensions do), any
// difference fails closed because no rule says which row is right.
func foldRawFields(existing, incoming map[string]json.RawMessage, rules map[string]strategy.SeriesFoldRule, kind string) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage, len(existing)+len(incoming))
	for name, value := range existing {
		result[name] = value
	}
	for name, value := range incoming {
		current, present := result[name]
		if !present {
			result[name] = value
			continue
		}
		rule, declared := rules[name]
		if !declared {
			if !bytes.Equal(current, value) {
				return nil, fmt.Errorf("alarmd worker: undeclared %s field %s differs between folded rows", kind, name)
			}
			continue
		}
		folded, err := foldRawValue(rule, current, value)
		if err != nil {
			return nil, fmt.Errorf("alarmd worker: fold %s field %s: %w", kind, name, err)
		}
		result[name] = folded
	}
	return result, nil
}

func foldRawValue(rule strategy.SeriesFoldRule, existing, incoming json.RawMessage) (json.RawMessage, error) {
	switch rule {
	case strategy.SeriesFoldMin:
		return foldMin(existing, incoming)
	case strategy.SeriesFoldUnionSet:
		return foldUnionSet(existing, incoming)
	case strategy.SeriesFoldDistinctValues:
		return foldDistinctValues(existing, incoming)
	default:
		return nil, fmt.Errorf("unknown fold rule %q", rule)
	}
}

// foldMin keeps the encoding of the numerically smaller value.
func foldMin(existing, incoming json.RawMessage) (json.RawMessage, error) {
	left, err := numericFoldValue(existing)
	if err != nil {
		return nil, err
	}
	right, err := numericFoldValue(incoming)
	if err != nil {
		return nil, err
	}
	if right < left {
		return incoming, nil
	}
	return existing, nil
}

func numericFoldValue(raw json.RawMessage) (float64, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("value is not JSON: %w", err)
	}
	switch typed := value.(type) {
	case json.Number:
		return typed.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(typed), 64)
	default:
		return 0, errors.New("value is not numeric")
	}
}

// foldUnionSet unions two set-valued dimensions encoded as JSON strings that
// carry a bracketed, comma separated list ("[80, 443]"). "[]", "null" and a
// JSON null are the empty set. When the union equals one side's entries that
// side's encoding is kept verbatim; otherwise the union is re-encoded as
// "[a,b,c]" in first-appearance order.
func foldUnionSet(existing, incoming json.RawMessage) (json.RawMessage, error) {
	left, err := setFoldEntries(existing)
	if err != nil {
		return nil, err
	}
	right, err := setFoldEntries(incoming)
	if err != nil {
		return nil, err
	}
	union := append([]string(nil), left...)
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, entry := range left {
		seen[entry] = struct{}{}
	}
	for _, entry := range right {
		if _, duplicate := seen[entry]; duplicate {
			continue
		}
		seen[entry] = struct{}{}
		union = append(union, entry)
	}
	switch {
	case len(union) == len(left):
		return existing, nil
	case len(union) == len(right) && len(left) == 0:
		return incoming, nil
	}
	return json.Marshal("[" + strings.Join(union, ",") + "]")
}

func setFoldEntries(raw json.RawMessage) ([]string, error) {
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("set value is not a JSON string: %w", err)
	}
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if text == "" || text == "[]" || text == "null" {
		return nil, nil
	}
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return nil, fmt.Errorf("set value %q is not a bracketed list", text)
	}
	entries := make([]string, 0)
	for _, entry := range strings.Split(text[1:len(text)-1], ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// foldDistinctValues keeps every distinct value of a scalar dimension in row
// order: the scalar itself when both rows agree, otherwise a JSON array of the
// distinct values. A side that is already such an array contributes its
// elements, so folding a third row extends the list.
func foldDistinctValues(existing, incoming json.RawMessage) (json.RawMessage, error) {
	left, err := distinctFoldValues(existing)
	if err != nil {
		return nil, err
	}
	right, err := distinctFoldValues(incoming)
	if err != nil {
		return nil, err
	}
	distinct := make([]json.RawMessage, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range append(left, right...) {
		key := string(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		distinct = append(distinct, value)
	}
	if len(distinct) == 1 {
		return distinct[0], nil
	}
	return json.Marshal(distinct)
}

func distinctFoldValues(raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("scalar value is empty")
	}
	if trimmed[0] != '[' {
		if !json.Valid(trimmed) {
			return nil, errors.New("scalar value is not JSON")
		}
		return []json.RawMessage{trimmed}, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("value list is not a JSON array: %w", err)
	}
	for index := range values {
		values[index] = bytes.TrimSpace(values[index])
	}
	return values, nil
}
