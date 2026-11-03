// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// writerHostRecords are host hash fields the CMDB cache writer publishes,
// produced by running the writer's own serializer over the scenarios of its
// own tests and over variants that reach the serializer's other branches -
// not written by hand here. Addresses are documentation addresses. They are
// regenerated from the writer's source whenever the writer changes.
type writerHostRecords struct {
	SourceCommit string `json:"source_commit"`
	Samples      []struct {
		Case    string `json:"case"`
		Field   string `json:"field"`
		Payload string `json:"payload"`
	} `json:"samples"`
}

// writerHost is what a record says about its host, read with a generic
// decoder rather than the reader under test.
type writerHost struct {
	id, ip, cloud, business, model, instance string
	nodes, unindexed                         []string
}

func readWriterHost(t *testing.T, payload string) writerHost {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("the writer published a record that is not JSON: %v", err)
	}
	text := func(value any) string {
		switch typed := value.(type) {
		case json.Number:
			return typed.String()
		case string:
			return typed
		}
		return ""
	}
	host := writerHost{id: text(record["bk_host_id"]), ip: text(record["bk_host_innerip"]), cloud: text(record["bk_cloud_id"]),
		business: text(record["bk_biz_id"]), model: text(record["model_id"]), instance: text(record["model_inst_id"])}
	links, _ := record["topo_link"].(map[string]any)
	seen := map[string]bool{}
	for _, link := range links {
		path, _ := link.([]any)
		for _, raw := range path {
			node, _ := raw.(map[string]any)
			key := text(node["bk_obj_id"]) + "|" + text(node["bk_inst_id"])
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, numeric := node["bk_inst_id"].(json.Number); numeric {
				host.nodes = append(host.nodes, key)
			} else {
				host.unindexed = append(host.unindexed, key)
			}
		}
	}
	sort.Strings(host.nodes)
	sort.Strings(host.unindexed)
	return host
}

// Every host record the writer publishes is read: none is refused, every
// field it is published under finds the host, and the facts a filter and a
// target read - id, address, area, business, the canonical instance, the
// topology nodes - are the ones the record carries. A topology node whose
// instance id the writer leaves as text is not indexed; that is held to a
// list, so a writer that starts publishing one is seen here.
func TestEveryHostRecordTheWriterPublishesIsRead(t *testing.T) {
	payload, err := os.ReadFile("testdata/writer-host-records.json")
	if err != nil {
		t.Fatal(err)
	}
	var records writerHostRecords
	if err := json.Unmarshal(payload, &records); err != nil {
		t.Fatal(err)
	}
	if len(records.Samples) == 0 {
		t.Fatal("no writer records")
	}

	fields := make([]string, 0, 2*len(records.Samples))
	refused := 0
	hosts := map[string]bool{}
	for _, sample := range records.Samples {
		if _, err := decodeHost(sample.Payload); err != nil {
			refused++
			t.Errorf("%s: the reader refuses the record the writer published under %q: %v", sample.Case, sample.Field, err)
		}
		fields = append(fields, sample.Field, sample.Payload)
		hosts[readWriterHost(t, sample.Payload).id] = true
	}
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addFields(fields)
	index := builder.index
	if index.Hosts() != len(hosts) {
		t.Errorf("index holds %d hosts, the writer published %d", index.Hosts(), len(hosts))
	}

	var unindexed []string
	for _, sample := range records.Samples {
		want := readWriterHost(t, sample.Payload)
		got, found := index.Lookup(sample.Field)
		if !found {
			t.Errorf("%s: no host under the field %q the writer published it under", sample.Case, sample.Field)
			continue
		}
		if got.HostID != want.id || got.IP != want.ip || got.CloudID != want.cloud || got.BusinessID != want.business ||
			got.ModelID != want.model || got.ModelInstID != want.instance {
			t.Errorf("%s: read as id=%s ip=%s cloud=%s business=%s model=%s/%s, the writer published %+v",
				sample.Case, got.HostID, got.IP, got.CloudID, got.BusinessID, got.ModelID, got.ModelInstID, want)
		}
		nodes := append([]string(nil), got.TopoNodes...)
		sort.Strings(nodes)
		if strings.Join(nodes, ",") != strings.Join(want.nodes, ",") {
			t.Errorf("%s: topology read as %v, the writer published %v", sample.Case, nodes, want.nodes)
		}
		if sample.Field == want.id {
			unindexed = append(unindexed, want.unindexed...)
		}
	}
	if strings.Join(unindexed, ",") != "custom_level|rack-a" {
		t.Errorf("topology nodes with a text instance id: %v, want the one known case", unindexed)
	}
	// The load counts that node as refused, and no record.
	if got := index.Refused(); got.Hosts != 0 || got.ServiceInstances != 0 || got.TopoNodes != len(unindexed) {
		t.Errorf("refused = %+v, want no record and the %d known node", got, len(unindexed))
	}
	t.Logf("writer %s: %d fields, %d hosts, %d refused", records.SourceCommit[:10], len(records.Samples), len(hosts), refused)
}
