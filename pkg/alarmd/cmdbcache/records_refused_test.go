// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// refusalHost is a host record with the given topology links, as the writer
// publishes one.
func refusalHost(id, ip, links string) string {
	return `{"bk_host_id":` + id + `,"bk_host_innerip":"` + ip + `","bk_cloud_id":0,"bk_biz_id":2,"topo_link":{` + links + `}}`
}

const refusalChain = `"module|1":[{"bk_obj_id":"module","bk_inst_id":1},{"bk_obj_id":"set","bk_inst_id":4},{"bk_obj_id":"biz","bk_inst_id":2}]`

func assertRefused(t *testing.T, got, want RefusedRecords) {
	t.Helper()
	if got != want {
		t.Fatalf("refused = %+v, want %+v", got, want)
	}
}

// Host records that do not decode, between ones that do, are counted and
// the first is named by its field; the ones around them are indexed as
// before.
func TestAHostRecordThatDoesNotDecodeIsCountedAndTheOthersIndexed(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addFields([]string{
		"192.0.2.1|0", refusalHost("1", "192.0.2.1", refusalChain),
		"192.0.2.2|0", `{"bk_host_id":2,"bk_host_innerip":`,
		"192.0.2.3|0", refusalHost("3", "192.0.2.3", refusalChain),
		"192.0.2.4|0", `not a record`,
	})
	index := builder.index
	assertRefused(t, index.Refused(), RefusedRecords{Hosts: 2, FirstHost: "192.0.2.2|0"})
	if index.Hosts() != 2 {
		t.Fatalf("hosts = %d, want the 2 that decode", index.Hosts())
	}
	for _, field := range []string{"192.0.2.1|0", "192.0.2.3|0"} {
		if _, found := index.Lookup(field); !found {
			t.Fatalf("%s is not indexed beside the refused record", field)
		}
	}
	if _, found := index.Lookup("192.0.2.2|0"); found {
		t.Fatal("the refused record is indexed")
	}
}

// Service instance records that do not decode are counted and the first is
// named by its field, and the instances around them are indexed.
func TestAServiceInstanceRecordThatDoesNotDecodeIsCountedAndTheOthersIndexed(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addServiceInstanceFields([]string{
		"11", `{"service_instance_id":11,"bk_host_id":1,"ip":"192.0.2.1","topo_link":{` + refusalChain + `}}`,
		"12", `not a record`,
		"13", `{"service_instance_id":13,"bk_host_id":3,"ip":"192.0.2.3"}`,
		"14", `{"service_instance_id":`,
	})
	index := builder.index
	assertRefused(t, index.Refused(), RefusedRecords{ServiceInstances: 2, FirstServiceInstance: "12"})
	for _, id := range []string{"11", "13"} {
		if _, found := index.LookupServiceInstance(id); !found {
			t.Fatalf("instance %s is not indexed beside the refused record", id)
		}
	}
	if index.ServiceInstances() != 2 {
		t.Fatalf("instances = %d, want 2", index.ServiceInstances())
	}
}

// A topology node that does not decode to an object and a numeric instance
// is counted, once for the record however many of its chains share it and
// once for the host however many fields it is published under; the host
// keeps every other node. A node with no object is refused as well as one
// whose instance is text.
func TestATopologyNodeThatDoesNotDecodeIsCountedOncePerRecord(t *testing.T) {
	shared := `{"bk_obj_id":"set","bk_inst_id":"rack-a"}`
	twoChains := `"module|1":[{"bk_obj_id":"module","bk_inst_id":1},` + shared + `,{"bk_obj_id":"biz","bk_inst_id":2}],` +
		`"module|3":[{"bk_obj_id":"module","bk_inst_id":3},` + shared + `,{"bk_obj_id":"biz","bk_inst_id":2}]`
	noObject := `"module|5":[{"bk_obj_id":"module","bk_inst_id":5},{"bk_inst_id":6},{"bk_obj_id":"biz","bk_inst_id":2}]`
	first := refusalHost("1", "192.0.2.1", twoChains)
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addFields([]string{
		"192.0.2.1|0", first,
		"1", first,
		"192.0.2.5|0", refusalHost("5", "192.0.2.5", noObject),
	})
	index := builder.index
	assertRefused(t, index.Refused(), RefusedRecords{TopoNodes: 2, FirstTopoNode: "host:192.0.2.1|0"})
	for field, want := range map[string]string{"1": "biz|2,module|1,module|3", "192.0.2.5|0": "biz|2,module|5"} {
		host, found := index.Lookup(field)
		if !found {
			t.Fatalf("the host under %s is not indexed", field)
		}
		nodes := append([]string(nil), host.TopoNodes...)
		sort.Strings(nodes)
		if strings.Join(nodes, ",") != want {
			t.Fatalf("%s nodes = %v, want %s: every node but the refused one", field, nodes, want)
		}
	}

	// An instance's node counts on the same line, and the first stays the
	// host's that came first.
	builder.addServiceInstanceFields([]string{
		"11", `{"service_instance_id":11,"bk_host_id":1,"topo_link":{"module|1":[{"bk_obj_id":"module","bk_inst_id":"x"}]}}`,
	})
	assertRefused(t, builder.index.Refused(), RefusedRecords{TopoNodes: 3, FirstTopoNode: "host:192.0.2.1|0"})
	instances := newIndexBuilder(time.Unix(1_700_000_000, 0))
	instances.addServiceInstanceFields([]string{
		"11", `{"service_instance_id":11,"bk_host_id":1,"topo_link":{"module|1":[{"bk_obj_id":"module","bk_inst_id":"x"}]}}`,
	})
	assertRefused(t, instances.index.Refused(), RefusedRecords{TopoNodes: 1, FirstTopoNode: "service_instance:11"})
}

// Records the writer got right refuse nothing.
func TestCleanRecordsRefuseNothing(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addFields([]string{"192.0.2.1|0", refusalHost("1", "192.0.2.1", refusalChain), "1", refusalHost("1", "192.0.2.1", refusalChain)})
	builder.addServiceInstanceFields([]string{"11", `{"service_instance_id":11,"bk_host_id":1,"topo_link":{` + refusalChain + `}}`})
	assertRefused(t, builder.index.Refused(), RefusedRecords{})
}

// A field kept to name a refused record is bounded, on both sides of the
// bound, and stays text: one of exactly the bound is kept whole, one a byte
// over is cut to the bound, and a cut that would split a character falls
// back to the character's start. The record a refused node was in is named
// under the same bound.
func TestTheFieldKeptForARefusedRecordIsBounded(t *testing.T) {
	at := strings.Repeat("a", maxRefusedFieldBytes)
	over := at + "b"
	split := strings.Repeat("a", maxRefusedFieldBytes-1) + "主机"
	for field, want := range map[string]string{at: at, over: at, split: strings.Repeat("a", maxRefusedFieldBytes-1)} {
		builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
		builder.addFields([]string{field, "{"})
		if kept := builder.index.Refused().FirstHost; kept != want || !utf8.ValidString(kept) {
			t.Fatalf("a %d-byte field kept as %d bytes, want %d", len(field), len(kept), len(want))
		}
	}
	builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
	builder.addFields([]string{over, refusalHost("1", "192.0.2.1", `"module|1":[{"bk_obj_id":"module","bk_inst_id":"x"}]`)})
	if kept := builder.index.Refused().FirstTopoNode; kept != "host:"+at {
		t.Fatalf("the record a refused node was in is named in %d bytes, want the field cut to %d", len(kept), maxRefusedFieldBytes)
	}
}

// scriptedLoads hands the store one index or error per load, in order.
type scriptedLoads struct {
	loads []func() (*Index, error)
}

func (script *scriptedLoads) Load(context.Context, time.Time) (*Index, error) {
	next := script.loads[0]
	script.loads = script.loads[1:]
	return next()
}

func loadOf(hosts []string, instances ...string) func() (*Index, error) {
	return func() (*Index, error) {
		builder := newIndexBuilder(time.Unix(1_700_000_000, 0))
		builder.addFields(hosts)
		builder.addServiceInstanceFields(instances)
		return builder.index, nil
	}
}

// Health carries what the held index's load refused, back to none once a
// load reads clean; a failed load keeps the held index's counts. The store
// says so when any of the three counts changes - the first load's from
// none - and not on a load that refuses as many again.
func TestTheStoreSaysWhatALoadRefusedWhenItChanges(t *testing.T) {
	good := []string{"192.0.2.1|0", refusalHost("1", "192.0.2.1", refusalChain)}
	dirty := append([]string{"192.0.2.2|0", "{"}, good...)
	badNode := []string{"11", `{"service_instance_id":11,"bk_host_id":1,"topo_link":{"module|1":[{"bk_obj_id":"module","bk_inst_id":"x"}]}}`}
	badInstance := append([]string{"12", "not a record"}, badNode...)
	script := &scriptedLoads{loads: []func() (*Index, error){
		loadOf(dirty), loadOf(dirty), func() (*Index, error) { return nil, errors.New("scan failed") },
		loadOf(dirty, badNode...), loadOf(dirty, badInstance...), loadOf(good), loadOf(good),
	}}
	var said []RefusedRecords
	store, err := NewStore(script, StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Hour,
		RefusalsChanged: func(refused RefusedRecords) { said = append(said, refused) }})
	if err != nil {
		t.Fatal(err)
	}
	hosts := RefusedRecords{Hosts: 1, FirstHost: "192.0.2.2|0"}
	nodes := RefusedRecords{Hosts: 1, FirstHost: "192.0.2.2|0", TopoNodes: 1, FirstTopoNode: "service_instance:11"}
	instances := RefusedRecords{Hosts: 1, FirstHost: "192.0.2.2|0", ServiceInstances: 1, FirstServiceInstance: "12",
		TopoNodes: 1, FirstTopoNode: "service_instance:11"}
	steps := []struct {
		failed bool
		health RefusedRecords
		said   int
	}{
		{health: hosts, said: 1},
		{health: hosts, said: 1},
		{failed: true, health: hosts, said: 1},
		// Only the node count changes, then only the instance count.
		{health: nodes, said: 2},
		{health: instances, said: 3},
		{health: RefusedRecords{}, said: 4},
		{health: RefusedRecords{}, said: 4},
	}
	for step, want := range steps {
		if err := store.Refresh(context.Background()); (err != nil) != want.failed {
			t.Fatalf("load %d: err = %v", step+1, err)
		}
		assertRefused(t, store.Health().Refused, want.health)
		if len(said) != want.said {
			t.Fatalf("load %d: said %d times %v, want %d", step+1, len(said), said, want.said)
		}
	}
	for index, want := range []RefusedRecords{hosts, nodes, instances, {}} {
		assertRefused(t, said[index], want)
	}
}
