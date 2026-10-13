// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package elasticsearch

import (
	"encoding/json"
	"fmt"

	elastic "github.com/olivere/elastic/v7"
)

// termsAggregationsComplete follows the requested aggregation tree, including
// every parent bucket. Histogram and nested nodes do not have terms error counts.
func termsAggregationsComplete(data elastic.Aggregations, infos aggInfoList) (bool, error) {
	if len(infos) == 0 {
		return true, nil
	}
	var name string
	terms := false
	buckets := false
	switch info := infos[len(infos)-1].(type) {
	case TermAgg:
		name, terms, buckets = info.Name, true, true
	case TimeAgg:
		name, buckets = info.Name, true
	case NestedAgg:
		name = info.Name
	case ReverNested:
		name = info.Name
	case ValueAgg:
		return true, nil
	default:
		return false, fmt.Errorf("ES aggregation completeness is unknown for %T", info)
	}
	raw, ok := data[name]
	if !ok {
		return false, fmt.Errorf("ES aggregation completeness is unknown for %s", name)
	}
	if !buckets {
		var child elastic.Aggregations
		if err := json.Unmarshal(raw, &child); err != nil {
			return false, err
		}
		return termsAggregationsComplete(child, infos[:len(infos)-1])
	}
	var node struct {
		Other   *int64             `json:"sum_other_doc_count"`
		Error   *int64             `json:"doc_count_error_upper_bound"`
		Buckets *[]json.RawMessage `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return false, err
	}
	if node.Buckets == nil {
		return false, fmt.Errorf("ES aggregation buckets are unknown for %s", name)
	}
	if terms {
		if node.Other == nil || node.Error == nil || *node.Other < 0 || *node.Error < 0 {
			return false, fmt.Errorf("ES terms completeness is unknown for %s", name)
		}
		if *node.Other > 0 || *node.Error > 0 {
			return false, nil
		}
	}
	for _, bucket := range *node.Buckets {
		var child elastic.Aggregations
		if err := json.Unmarshal(bucket, &child); err != nil {
			return false, err
		}
		complete, err := termsAggregationsComplete(child, infos[:len(infos)-1])
		if err != nil || !complete {
			return complete, err
		}
	}
	return true, nil
}
