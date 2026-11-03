// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package linkdoutput

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The link searches labels, never extra_data, so the facts an event
// carries are also a label: the digest of the very
// extra_data.strategy_facts bytes. A no-data event digests its no-data
// grouping, so its bucket is not the threshold one's. An event without
// facts writes no digest, and a close carries only the three labels the
// enrichment reads.
func TestADetectionEventLabelsItsFactsDigest(t *testing.T) {
	facts := contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c", ConditionBasis: "query",
		DimensionDigest: "threshold", NoDataDimensionDigest: "no-data"}
	digests := map[string]bool{}
	for _, noData := range []bool{false, true} {
		message := convert(t, decision(func(e *contract.TriggerEventV1) {
			e.StrategyFacts = &facts
			if noData {
				e.RecordRef.Dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
				e.Observed = contract.TriggerObservedV1{Values: map[string]json.RawMessage{contract.NoDataPeriodFactField: json.RawMessage("5")}}
			}
		}))
		var labels map[string]any
		var extra struct {
			Facts json.RawMessage `json:"strategy_facts"`
		}
		if json.Unmarshal(message["labels"], &labels) != nil || json.Unmarshal(message["extra_data"], &extra) != nil {
			t.Fatalf("labels %s, extra_data %s", message["labels"], message["extra_data"])
		}
		sum := md5.Sum(extra.Facts)
		if labels["strategy_facts_digest"] != hex.EncodeToString(sum[:]) || len(labels) != 4 {
			t.Fatalf("no-data %v: labels %s, want the three and the MD5 of %s", noData, message["labels"], extra.Facts)
		}
		digests[labels["strategy_facts_digest"].(string)] = true
	}
	if len(digests) != 2 {
		t.Fatalf("digests %v, want the threshold and the no-data event in different buckets", digests)
	}
	bare := convert(t, decision(nil))
	if strings.Contains(string(bare["labels"]), "strategy_facts_digest") {
		t.Fatalf("labels = %s, want no digest on an event without facts", bare["labels"])
	}
	closed, err := ConvertClose(CloseRequest{TenantID: "tenant-a", Fingerprint: strings.Repeat("b", 32), AlertInstanceID: "alert-1",
		StrategyID: 123, StrategyRevision: 7, BusinessID: 2, OccurredAt: time.Unix(1756684800, 0)})
	if err != nil {
		t.Fatal(err)
	}
	var close struct {
		Labels map[string]any `json:"labels"`
	}
	if json.Unmarshal(closed.Payload, &close) != nil || len(close.Labels) != 3 {
		t.Fatalf("close labels = %v, want only the three the enrichment reads", close.Labels)
	}
}
