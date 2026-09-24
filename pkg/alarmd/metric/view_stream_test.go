// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"
)

func gatherViewStream(t *testing.T, source func() ViewStreamCounts) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	recorder.SetViewStreamSource(source)
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				key += pair.GetValue()
			}
			if gathered[family.GetName()] == nil {
				gathered[family.GetName()] = map[string]float64{}
			}
			value := series.GetGauge().GetValue()
			if series.Counter != nil {
				value = series.GetCounter().GetValue()
			}
			gathered[family.GetName()][key] = value
		}
	}
	return gathered
}

// A Leader publishes the four numbers of its current version by stage,
// the receipts it ignored by reason, and what it sent; a follower
// publishes only what it counted across terms and leading 0, because the
// four numbers are a Leader's and zeros on a follower would read as a
// Leader nobody has installed from.
func TestTheLeaderPublishesTheFourNumbersAndAFollowerOnlyItsCounters(t *testing.T) {
	leader := gatherViewStream(t, func() ViewStreamCounts {
		return ViewStreamCounts{Leading: true, Revision: 12, Sessions: 63,
			Expected: 64, Sent: 64, Acked: 63, Installed: 61, Switched: 0,
			IgnoredUnknownVersion: 1, IgnoredDigestMismatch: 2,
			Publications: 12, PublicationsSkipped: 700, SnapshotChunksSent: 64, DeltasSent: 30, EmptyDeltasSent: 600, DeltasOversized: 2, Refusals: 3}
	})
	if got := leader["bkmonitor_alarmd_view_version_receivers"]; got["expected"] != 64 || got["sent"] != 64 || got["acked"] != 63 || got["installed"] != 61 || got["switched"] != 0 || len(got) != 5 {
		t.Fatalf("receivers = %v", got)
	}
	if got := leader["bkmonitor_alarmd_view_receipts_ignored_total"]; got["unknown_version"] != 1 || got["digest_mismatch"] != 2 || got["unexpected_receiver"] != 0 || got["stale_incarnation"] != 0 || len(got) != 4 {
		t.Fatalf("ignored = %v", got)
	}
	if leader["bkmonitor_alarmd_view_stream_leading"][""] != 1 || leader["bkmonitor_alarmd_view_revision"][""] != 12 || leader["bkmonitor_alarmd_view_stream_sessions"][""] != 63 {
		t.Fatalf("leader gauges = %v %v %v", leader["bkmonitor_alarmd_view_stream_leading"], leader["bkmonitor_alarmd_view_revision"], leader["bkmonitor_alarmd_view_stream_sessions"])
	}
	if got := leader["bkmonitor_alarmd_view_publications_total"]; got["changed"] != 12 || got["unchanged"] != 700 {
		t.Fatalf("publications = %v", got)
	}
	if got := leader["bkmonitor_alarmd_view_messages_sent_total"]; got["snapshot_chunk"] != 64 || got["delta"] != 30 || got["empty_delta"] != 600 {
		t.Fatalf("sent = %v", got)
	}
	if leader["bkmonitor_alarmd_view_stream_refusals_total"][""] != 3 {
		t.Fatalf("refusals = %v", leader["bkmonitor_alarmd_view_stream_refusals_total"])
	}
	if leader["bkmonitor_alarmd_view_deltas_oversized_total"][""] != 2 {
		t.Fatalf("oversized deltas = %v", leader["bkmonitor_alarmd_view_deltas_oversized_total"])
	}
	follower := gatherViewStream(t, func() ViewStreamCounts { return ViewStreamCounts{Refusals: 1, PublicationsSkipped: 5} })
	if follower["bkmonitor_alarmd_view_stream_leading"][""] != 0 || follower["bkmonitor_alarmd_view_stream_refusals_total"][""] != 1 {
		t.Fatalf("follower leading/refusals = %v %v", follower["bkmonitor_alarmd_view_stream_leading"], follower["bkmonitor_alarmd_view_stream_refusals_total"])
	}
	for _, name := range []string{"bkmonitor_alarmd_view_version_receivers", "bkmonitor_alarmd_view_receipts_ignored_total", "bkmonitor_alarmd_view_revision", "bkmonitor_alarmd_view_stream_sessions"} {
		if series, ok := follower[name]; ok {
			t.Errorf("a follower published %s = %v; the four numbers are the Leader's", name, series)
		}
	}
	none := gatherViewStream(t, nil)
	if _, ok := none["bkmonitor_alarmd_view_stream_leading"]; ok {
		t.Error("a process without a stream published view series")
	}
}
