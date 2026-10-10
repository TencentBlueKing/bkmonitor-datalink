// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// addFieldsDecodingEveryRecord is how host records were filed before the
// second copy of a host was recognised before it was read: every record read
// whole, then dropped when its host id was already filed. The reference the
// current reading is held to. What it refuses is counted as the current
// reading counts it: a record that does not decode under every field, a
// topology node once for the host it files.
func (builder *indexBuilder) addFieldsDecodingEveryRecord(fields []string) {
	for position := 0; position+1 < len(fields); position += 2 {
		identity, payload := fields[position], fields[position+1]
		wire, err := decodeWireHost(payload)
		if err != nil {
			builder.index.refused.host(identity)
			continue
		}
		facts, refusedNodes := hostFactsOf(wire, payload)
		if parsed, err := strconv.ParseInt(facts.HostID, 10, 64); err != nil || parsed <= 0 {
			builder.index.hostIDsIncomplete = true
		}
		if facts.HostID != "" {
			if existing, found := builder.seen[facts.HostID]; found {
				builder.index.byIdentity[identity] = existing
				continue
			}
			builder.seen[facts.HostID] = facts
		}
		builder.index.refused.topoNodes("host", identity, refusedNodes)
		builder.index.hosts++
		builder.index.byIdentity[identity] = facts
		builder.addToNodes(facts)
		if facts.ModelID != "" && facts.ModelInstID != "" && facts.HostID != "" {
			builder.index.modelledHosts++
			builder.index.byModelInstance[facts.ModelID+"|"+facts.ModelInstID] = facts
		}
	}
}

// sortHostNodes puts every filed host's nodes in one order.
func sortHostNodes(index *Index) {
	for _, facts := range index.byIdentity {
		sort.Strings(facts.TopoNodes)
	}
}

// dedupCorpus is host records as the writer puts them and as it should not:
// every shape the reading decides differently on.
var dedupCorpus = []string{
	// One host under both of its keys, the same record twice.
	"192.0.2.1|0", `{"bk_host_id":7,"bk_host_innerip":"192.0.2.1","bk_cloud_id":0,"bk_biz_id":2,"os":"linux","topo_link":{"module|5":[{"bk_obj_id":"module","bk_inst_id":5},{"bk_obj_id":"set","bk_inst_id":3}]}}`,
	"7", `{"bk_host_id":7,"bk_host_innerip":"192.0.2.1","bk_cloud_id":0,"bk_biz_id":2,"os":"linux","topo_link":{"module|5":[{"bk_obj_id":"module","bk_inst_id":5},{"bk_obj_id":"set","bk_inst_id":3}]}}`,
	// One host under both keys with records that differ: the first read wins.
	"192.0.2.2|0", `{"bk_host_id":8,"bk_host_innerip":"192.0.2.2","bk_biz_id":2,"rack":"a1"}`,
	"8", `{"bk_host_id":8,"bk_host_innerip":"192.0.2.2","bk_biz_id":3,"rack":"b9","model_id":"host","model_inst_id":8}`,
	// Hosts without a host id are never merged: each counts.
	"192.0.2.3|0", `{"bk_host_innerip":"192.0.2.3","bk_biz_id":2}`,
	"192.0.2.3|1", `{"bk_host_innerip":"192.0.2.3","bk_cloud_id":1,"bk_biz_id":2}`,
	// Not JSON: skipped, under either key.
	"192.0.2.4|0", `{"bk_host_id":9,`,
	// JSON the host fields refuse, sharing a host id already filed: skipped
	// as it was, not filed under the first copy.
	"192.0.2.5|0", `{"bk_host_id":10,"bk_host_innerip":"192.0.2.5","bk_biz_id":2}`,
	"10", `{"bk_host_id":10,"bk_host_innerip":"192.0.2.5","bk_biz_id":{"not":"a number"}}`,
	// A field name in another case and a repeated key: the decoder's rules
	// decide the host id, the same way for both readings.
	"192.0.2.6|0", `{"BK_HOST_ID":11,"bk_host_innerip":"192.0.2.6","bk_biz_id":2}`,
	"11", `{"bk_host_id":11,"bk_host_innerip":"192.0.2.6","bk_biz_id":4}`,
	"192.0.2.7|0", `{"bk_host_id":12,"bk_host_id":13,"bk_host_innerip":"192.0.2.7","bk_biz_id":2}`,
	"13", `{"bk_host_id":13,"bk_host_innerip":"192.0.2.7","bk_biz_id":5}`,
	"12", `{"bk_host_id":12,"bk_host_innerip":"192.0.2.8","bk_biz_id":2}`,
	// A host id written as a number with a fraction, and its model identity.
	"192.0.2.9|0", `{"bk_host_id":14.0,"bk_host_innerip":"192.0.2.9","bk_biz_id":2,"model_id":"host","model_inst_id":"14"}`,
	"14", `{"bk_host_id":14,"bk_host_innerip":"192.0.2.9","bk_biz_id":6,"model_id":"host","model_inst_id":"14"}`,
}

// Recognising a host's second copy by its host id before reading the rest
// of it builds the same index, field for field, as reading every record
// whole: the same identities, the same records behind them -- each
// identity of a host resolving to the one record its first copy made --
// the same count, nodes and model identities.
func TestAHostsSecondCopyIsRecognisedBeforeItIsReadAndTheIndexIsTheSame(t *testing.T) {
	at := time.Unix(1_788_000_000, 0)
	for _, pageSize := range []int{2, 4, len(dedupCorpus)} {
		current, reference := newIndexBuilder(at), newIndexBuilder(at)
		for start := 0; start < len(dedupCorpus); start += pageSize {
			page := dedupCorpus[start:min(start+pageSize, len(dedupCorpus))]
			current.addFields(page)
			reference.addFieldsDecodingEveryRecord(page)
		}
		// A host's nodes come out of a map, in no order; each build orders
		// its own, so both are put in one order before they are compared.
		sortHostNodes(current.index)
		sortHostNodes(reference.index)
		if !reflect.DeepEqual(current.index, reference.index) {
			t.Fatalf("pages of %d: the index differs from reading every record whole:\n got  %+v\n want %+v", pageSize, current.index, reference.index)
		}
		for identity, facts := range reference.index.byIdentity {
			if facts.HostID == "" {
				continue
			}
			if current.index.byIdentity[identity] != current.seen[facts.HostID] {
				t.Fatalf("pages of %d: identity %s does not resolve to its host's one record", pageSize, identity)
			}
		}
	}
	// The corpus reaches every shape it names: nine hosts, the two without
	// a host id counted apart; the records the host fields refuse filed
	// under neither key; the first of two different copies winning; a host
	// id read in another case and from the last of repeated keys.
	built := newIndexBuilder(at)
	built.addFields(dedupCorpus)
	index := built.index
	if index.hosts != 9 {
		t.Fatalf("setup: the corpus files %d hosts, want 9", index.hosts)
	}
	for _, skipped := range []string{"192.0.2.4|0", "10"} {
		if _, filed := index.byIdentity[skipped]; filed {
			t.Fatalf("setup: %s was filed; its record is refused", skipped)
		}
	}
	if host := index.byIdentity["8"]; host == nil || host.Attributes["rack"] != "a1" {
		t.Fatalf("setup: host 8 = %+v, want its first copy's rack", host)
	}
	if host := index.byIdentity["11"]; host == nil || host != index.byIdentity["192.0.2.6|0"] {
		t.Fatal("setup: the host whose id was read in another case is not one record under both keys")
	}
	if host := index.byIdentity["13"]; host == nil || host != index.byIdentity["192.0.2.7|0"] || index.byIdentity["12"] == host {
		t.Fatal("setup: the host id of repeated keys is not the last one")
	}
}

// A host's second copy is not read past its host id: filing it allocates a
// fraction of what filing a new host of the same record does, which is
// building the topology and the attributes, the bulk of a refresh.
func TestAHostsSecondCopyIsNotReadPastItsHostID(t *testing.T) {
	var attributes strings.Builder
	for index := 0; index < 60; index++ {
		fmt.Fprintf(&attributes, `,"attribute_%d":"value %d"`, index, index)
	}
	record := `{"bk_host_id":7,"bk_host_innerip":"192.0.2.1","bk_biz_id":2` + attributes.String() + `}`
	at := time.Unix(1_788_000_000, 0)
	first := testing.AllocsPerRun(20, func() {
		newIndexBuilder(at).addFields([]string{"192.0.2.1|0", record})
	})
	second := testing.AllocsPerRun(20, func() {
		builder := newIndexBuilder(at)
		builder.seen["7"] = &HostFacts{HostID: "7"}
		builder.addFields([]string{"7", record})
	})
	if second*2 > first {
		t.Fatalf("filing a host's second copy allocates %.0f times against %.0f for its first: it was read whole", second, first)
	}
}
