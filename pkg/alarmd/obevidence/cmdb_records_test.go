// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obevidence

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
)

// hgetCalls is how many HGETs the server has run, from its own count: a read
// the command budget says was skipped is checked against the server, not
// against the count the code reports about itself.
func hgetCalls(ctx context.Context, t *testing.T, client *redis.Client) int64 {
	t.Helper()
	info, err := client.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(info, "cmdstat_hget:calls=")
	if !found {
		return 0
	}
	calls, err := strconv.ParseInt(rest[:strings.IndexByte(rest, ',')], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

// One CMDB record at a time, from the hash the index loads whole: a host by
// its id or by its "ip|cloud" field - the writer keys each host both ways -
// and a service instance by its id. The record comes back as written, with
// what alarmd decodes from it or why it cannot, which is what a load that
// skips it never says. A key that is not there and a record the hash does
// not hold are both missing, and say which; a record past the document limit
// is not read at all.
func TestACMDBRecordIsReadAsWrittenAndAsAlarmdDecodesIt(t *testing.T) {
	ctx := context.Background()
	client := redisForTest(t)
	const prefix = "bk_test.ee"
	hosts, instances := prefix+".cache.cmdb.host", prefix+".cache.cmdb.service_instance"
	record := `{"bk_host_id":101,"bk_host_innerip":"192.0.2.10","bk_cloud_id":0,"bk_biz_id":2,"bk_state":"运营中","topo_link":{"module|7":[{"bk_obj_id":"set","bk_inst_id":3},{"bk_obj_id":"module","bk_inst_id":7}]}}`
	if err := client.HSet(ctx, hosts, "101", record, "192.0.2.10|0", record, "102", "{not json", "104", "").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, hosts, "103", strings.Repeat("x", MaxDocumentBytes+1), "105", strings.Repeat("y", MaxDocumentBytes)).Err(); err != nil {
		t.Fatal(err)
	}
	// 502 names no id of its own: a load files it under its field. 503 names
	// one that is not its field, and keeps it.
	if err := client.HSet(ctx, instances, "501", `{"service_instance_id":501,"bk_host_id":101,"ip":"192.0.2.10","bk_cloud_id":0}`,
		"502", `{"bk_host_id":101,"ip":"192.0.2.10","bk_cloud_id":0}`,
		"503", `{"service_instance_id":9,"bk_host_id":101,"ip":"192.0.2.10","bk_cloud_id":0}`).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Expire(ctx, instances, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	reader, err := cmdbcache.NewReader(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	index, err := reader.Load(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := New(Options{CMDBCache: RedisBinding{Client: client, Location: Location{Role: "cmdb_cache", Prefix: prefix}}})
	host := func(field string) Result {
		return service.Store(ctx, StoreRequest{Family: FamilyCMDBHost, Host: field})
	}

	for _, field := range []string{"101", "192.0.2.10|0"} {
		r := host(field)
		got, _ := r.Value.(CMDBRecord)
		decoded, _ := got.Decoded.(map[string]any)
		if r.Status != "ok" || r.Location.Key != hosts || got.Field != field || decoded["host_id"] != "101" || decoded["ip"] != "192.0.2.10" ||
			r.Limits.Commands != 7 || r.Limits.Bytes != len(record) {
			t.Fatalf("host %s = %+v value %+v, want the record, decoded, in 7 commands", field, r, got)
		}
		if raw, ok := got.Record.(json.RawMessage); !ok || string(raw) != record {
			t.Fatalf("host %s record = %v, want the payload as written", field, got.Record)
		}
		// What a load holds for the host, field for field.
		loaded, found := index.Lookup(field)
		if !found {
			t.Fatalf("setup: the loaded index has no host %s", field)
		}
		// Topology nodes come out of a map, in no order: compared as sets.
		sorted := func(nodes any) []string {
			copied := append([]string(nil), nodes.([]string)...)
			slices.Sort(copied)
			return copied
		}
		want := map[string]any{"host_id": loaded.HostID, "ip": loaded.IP, "cloud_id": loaded.CloudID,
			"business_id": loaded.BusinessID, "topo_nodes": sorted(loaded.TopoNodes)}
		decoded["topo_nodes"] = sorted(decoded["topo_nodes"])
		if !reflect.DeepEqual(decoded, want) || decoded["business_id"] != "2" || len(loaded.TopoNodes) != 2 {
			t.Fatalf("host %s decoded = %v, want what the loaded index holds %v", field, decoded, want)
		}
		if r.TTLMS == nil || *r.TTLMS != -1 {
			t.Fatalf("host %s ttl = %v, want -1 for a hash with no expiry", field, r.TTLMS)
		}
	}
	if r := host("999"); r.Status != "missing" || r.Reason != "field_absent" || !r.Complete || r.Limits.Commands != 6 {
		t.Fatalf("a host the hash does not hold = %+v, want missing/field_absent without reading a value", r)
	}
	r := host("102")
	if got, _ := r.Value.(CMDBRecord); r.Status != "ok" || got.DecodeError == "" || got.Decoded != nil || got.Record != "{not json" {
		t.Fatalf("a record alarmd cannot decode = %+v value %+v, want it as text with the decode error", r, r.Value)
	}
	before := hgetCalls(ctx, t, client)
	if r := host("103"); r.Status != "budget_exceeded" || r.Reason != "document_bytes" || r.Value != nil || r.Limits.Commands != 6 {
		t.Fatalf("a record past the document limit = %+v, want refused unread", r)
	}
	if calls := hgetCalls(ctx, t, client) - before; calls != 0 {
		t.Fatalf("the server ran %d HGET for a record past the limit, want none", calls)
	}
	before = hgetCalls(ctx, t, client)
	if r := host("105"); r.Status != "ok" || r.Limits.Bytes != MaxDocumentBytes {
		t.Fatalf("a record of exactly the document limit = %+v, want read", r)
	}
	if calls := hgetCalls(ctx, t, client) - before; calls != 1 {
		t.Fatalf("the server ran %d HGET for a record at the limit, want one", calls)
	}
	if r := host("104"); r.Status != "empty" || !r.Complete {
		t.Fatalf("an empty record = %+v, want empty", r)
	}

	instance := service.Store(ctx, StoreRequest{Family: FamilyCMDBServiceInstance, ServiceInstance: "501"})
	if got, _ := instance.Value.(CMDBRecord); instance.Status != "ok" || instance.Location.Key != instances || got.Decoded.(map[string]any)["host_id"] != "101" {
		t.Fatalf("service instance 501 = %+v value %+v", instance, instance.Value)
	}
	if instance.TTLMS == nil || *instance.TTLMS <= 0 || *instance.TTLMS > time.Minute.Milliseconds() {
		t.Fatalf("service instance ttl = %v, want the hash's remaining minute", instance.TTLMS)
	}
	unnamed := service.Store(ctx, StoreRequest{Family: FamilyCMDBServiceInstance, ServiceInstance: "502"})
	loadedInstance, found := index.LookupServiceInstance("502")
	if got, _ := unnamed.Value.(CMDBRecord); unnamed.Status != "ok" || !found || got.Decoded.(map[string]any)["id"] != "502" || loadedInstance.ID != "502" {
		t.Fatalf("an instance naming no id = %+v value %+v, want its field's id, as the loaded index files it", unnamed, unnamed.Value)
	}
	named := service.Store(ctx, StoreRequest{Family: FamilyCMDBServiceInstance, ServiceInstance: "503"})
	loadedNamed, found := index.LookupServiceInstance("503")
	if got, _ := named.Value.(CMDBRecord); named.Status != "ok" || !found || got.Decoded.(map[string]any)["id"] != "9" || loadedNamed.ID != "9" {
		t.Fatalf("an instance naming its own id = %+v value %+v, want that id over its field's, as the loaded index keeps it", named, named.Value)
	}

	// The whole cache gone reads apart from one record gone.
	elsewhere := New(Options{CMDBCache: RedisBinding{Client: client, Location: Location{Role: "cmdb_cache", Prefix: "bk_other.ee"}}})
	if r := elsewhere.Store(ctx, StoreRequest{Family: FamilyCMDBHost, Host: "101"}); r.Status != "missing" || r.Reason != "key_absent" || !r.Complete {
		t.Fatalf("no host hash at all = %+v, want missing/key_absent", r)
	}
	if err := client.Set(ctx, "bk_wrong.ee.cache.cmdb.host", "a string", 0).Err(); err != nil {
		t.Fatal(err)
	}
	wrong := New(Options{CMDBCache: RedisBinding{Client: client, Location: Location{Role: "cmdb_cache", Prefix: "bk_wrong.ee"}}})
	if r := wrong.Store(ctx, StoreRequest{Family: FamilyCMDBHost, Host: "101"}); r.Status != "wrong_type" || r.Type != "string" {
		t.Fatalf("a host key that is not a hash = %+v, want wrong_type", r)
	}

	for name, request := range map[string]StoreRequest{
		"host not in either form":       {Family: FamilyCMDBHost, Host: "not-a-host"},
		"address without a cloud":       {Family: FamilyCMDBHost, Host: "192.0.2.10"},
		"not an address with a cloud":   {Family: FamilyCMDBHost, Host: "notanip|0"},
		"host with a strategy":          {Family: FamilyCMDBHost, Host: "101", StrategyID: "1"},
		"host asked as a service":       {Family: FamilyCMDBHost, ServiceInstance: "501"},
		"service instance of zero":      {Family: FamilyCMDBServiceInstance, ServiceInstance: "0"},
		"service instance with a host":  {Family: FamilyCMDBServiceInstance, ServiceInstance: "501", Host: "101"},
		"service instance not a number": {Family: FamilyCMDBServiceInstance, ServiceInstance: "abc"},
	} {
		if r := service.Store(ctx, request); r.Status != "invalid_input" {
			t.Errorf("%s = %+v, want invalid_input", name, r)
		}
	}
	unwired := New(Options{})
	if r := unwired.Store(ctx, StoreRequest{Family: FamilyCMDBHost, Host: "101"}); r.Status != "not_configured" {
		t.Fatalf("no CMDB binding = %+v, want not_configured", r)
	}
}

// grownRecord answers the value read with a record past the document limit,
// whatever length was asked first: the record grown between the two reads.
type grownRecord struct{ redis.Cmdable }

func (grownRecord) HGet(context.Context, string, string) *redis.StringCmd {
	return redis.NewStringResult(strings.Repeat("z", MaxDocumentBytes+1), nil)
}

// A record that grew past the limit between its length and its value is
// refused as it would have been, and not handed on whole.
func TestACMDBRecordGrownBetweenItsTwoReadsIsRefused(t *testing.T) {
	ctx := context.Background()
	client := redisForTest(t)
	if err := client.HSet(ctx, "bk_test.ee.cache.cmdb.host", "101", `{"bk_host_id":101}`).Err(); err != nil {
		t.Fatal(err)
	}
	service := New(Options{CMDBCache: RedisBinding{Client: grownRecord{client}, Location: Location{Role: "cmdb_cache", Prefix: "bk_test.ee"}}})
	if r := service.Store(ctx, StoreRequest{Family: FamilyCMDBHost, Host: "101"}); r.Status != "budget_exceeded" || r.Reason != "document_bytes" || r.Value != nil {
		t.Fatalf("a record grown past the limit = %+v, want refused", r)
	}
}
