// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// Use the publisher's literal keys rather than deriving fixtures from DBKey:
// the producer and consumer must agree even when fields belong to different domains.
func TestRedisSourceReadsPlatformDomainSettings(t *testing.T) {
	executable := redistest.Server(t)
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	waitRedisReady(t, client)
	ctx := context.Background()
	const prefix = "platform:"
	const root = prefix + "dynamic_config:"
	const hostKey = root + "{system}:base_config.metadata.host_disable_monitor_states"
	const diskKey = root + "{system}:base_config.domains.dataview.file_system_type_ignore"
	source, err := NewRedisSource(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	deploymentHosts := []string{"deployment-host-state"}
	cache, err := New(Options{Source: source, Deployment: Layer{HostDisableMonitorStates: &deploymentHosts}})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(values map[string]string, removed ...string) {
		t.Helper()
		_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			for key, value := range values {
				pipe.Set(ctx, key, value, 0)
			}
			if len(removed) != 0 {
				pipe.Del(ctx, removed...)
			}
			pipe.Set(ctx, root+"revision", "published", 0)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		cache.Refresh(ctx)
	}
	publish(map[string]string{
		hostKey: `["maintenance"]`,
		root + "{system}:base_config.metadata.is_access_bk_data":                `true`,
		root + "{system}:base_config.domains.strategy.bkdata_cmdb_level_tables": `["system.cpu_summary"]`,
		diskKey: `["tmpfs"]`,
		root + "{tenant-a}:base_config.metadata.host_disable_monitor_states": `["other-tenant"]`,
	})
	want := Settings{HostDisableMonitorStates: []string{"maintenance"}, IsAccessBKData: true,
		BKDataCMDBLevelTables: []string{"system.cpu_summary"}, FileSystemTypeIgnore: []string{"tmpfs"},
		NoDataTrackingHorizonSeconds: DefaultNoDataTrackingHorizonSeconds, NoDataTrackingHorizonSource: HorizonSourceDefault}
	if got := cache.Current(); !reflect.DeepEqual(got, want) || cache.Stats().Mode != ModeAuthoritative {
		t.Fatalf("published settings = %+v (%s), want %+v", got, cache.Stats().Mode, want)
	}
	// A periodic reread observes changed values even if the global revision is unchanged.
	publish(map[string]string{hostKey: `[]`, diskKey: `[]`})
	if got := cache.Current(); len(got.HostDisableMonitorStates) != 0 || len(got.FileSystemTypeIgnore) != 0 {
		t.Fatalf("empty overrides were lost: %+v", got)
	}
	publish(nil, hostKey)
	if got := cache.Current(); !reflect.DeepEqual(got.HostDisableMonitorStates, deploymentHosts) {
		t.Fatalf("deleted override did not fall back: %+v", got)
	}
	previous := cache.Current()
	publish(map[string]string{diskKey: `123`})
	if !reflect.DeepEqual(cache.Current(), previous) || cache.Stats().Mode != ModeStale {
		t.Fatalf("invalid publication replaced the last good settings: %+v", cache.Stats())
	}
	if err := client.Del(ctx, root+"revision").Err(); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(ctx)
	if !reflect.DeepEqual(cache.Current(), previous) || cache.Stats().Unavailable[UnavailableUnpublished] != 1 {
		t.Fatalf("missing revision reset settings: %+v", cache.Stats())
	}
}

// The Redis source against a real server, on the protocol's own keys and
// in its own shape (one pipeline: GET revision, MGET fields, GET revision
// again). Nothing
// published is Published false with no error; an absent field is absent; a
// present field comes back as the raw JSON the platform wrote, null
// included, so that "no override" and "present and empty" stay apart.
func TestRedisSourceReadsTheProtocolKeysInOneVersionCheckedPipeline(t *testing.T) {
	executable := redistest.Server(t)
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	waitRedisReady(t, client)
	ctx := context.Background()
	source, err := NewRedisSource(client, DefaultKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}

	publication, err := source.Read(ctx, Fields)
	if err != nil || publication.Published || len(publication.Values) != 0 {
		t.Fatalf("empty store: %+v, %v; want unpublished, no error, no values", publication, err)
	}
	// The platform publishes two fields and a null, then the revision.
	for key, value := range map[string]string{
		ConfigKey(DefaultKeyPrefix, Tenant, FieldHostDisableMonitorStates.DBKey()): `["备用机", "测试中"]`,
		ConfigKey(DefaultKeyPrefix, Tenant, FieldIsAccessBKData.DBKey()):           `true`,
		ConfigKey(DefaultKeyPrefix, Tenant, FieldFileSystemTypeIgnore.DBKey()):     `null`,
		RevisionKey(DefaultKeyPrefix):                                              "7d15372027f34084a760e6137675873f53",
	} {
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	publication, err = source.Read(ctx, Fields)
	if err != nil || !publication.Published || publication.Revision != "7d15372027f34084a760e6137675873f53" {
		t.Fatalf("published store: %+v, %v", publication, err)
	}
	if string(publication.Values[FieldHostDisableMonitorStates]) != `["备用机", "测试中"]` ||
		string(publication.Values[FieldIsAccessBKData]) != `true` ||
		string(publication.Values[FieldFileSystemTypeIgnore]) != `null` {
		t.Fatalf("values = %v", publication.Values)
	}
	if _, present := publication.Values[FieldBKDataCMDBLevelTables]; present {
		t.Fatal("an absent field is present")
	}
	// The override is removed the protocol's way, by DEL: absent again.
	if err := client.Del(ctx, ConfigKey(DefaultKeyPrefix, Tenant, FieldIsAccessBKData.DBKey())).Err(); err != nil {
		t.Fatal(err)
	}
	publication, err = source.Read(ctx, Fields)
	if _, present := publication.Values[FieldIsAccessBKData]; err != nil || present {
		t.Fatalf("after DEL: %+v, %v; want the field absent", publication, err)
	}
	// The whole thing through the cache: the platform's word over the
	// deployment's, and the deployment's back when the platform is silent.
	seven := []string{"备用机", "测试中", "故障中", "运营中[不监控]", "开发中[不监控]", "运营中[无告警]", "开发中[无告警]"}
	cache, err := New(Options{Source: source, Deployment: Layer{HostDisableMonitorStates: &seven}})
	if err != nil {
		t.Fatal(err)
	}
	cache.Refresh(ctx)
	if current := cache.Current(); cache.Stats().Mode != ModeAuthoritative || len(current.HostDisableMonitorStates) != 2 ||
		len(current.FileSystemTypeIgnore) != 0 {
		t.Fatalf("through the cache: mode %s current %+v", cache.Stats().Mode, current)
	}
	if err := client.Del(ctx, RevisionKey(DefaultKeyPrefix)).Err(); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(ctx)
	if stats := cache.Stats(); stats.Mode != ModeStale || stats.Unavailable[UnavailableUnpublished] != 1 {
		t.Fatalf("after the revision is gone: %+v, want stale for unpublished", stats)
	}
	if _, err := NewRedisSource(client, "bad{prefix}"); err == nil {
		t.Fatal("a prefix with braces was accepted")
	}
}

// tearingHook stands in for a publication landing between a read's first
// and last GET of the revision: for the first tears pipelines it changes
// the second GET's answer after Redis gave it. It also keeps each
// pipeline's command names.
type tearingHook struct {
	tears     int
	pipelines [][]string
}

func (*tearingHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*tearingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*tearingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (hook *tearingHook) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	names := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		names = append(names, cmd.Name())
	}
	hook.pipelines = append(hook.pipelines, names)
	if hook.tears > 0 {
		hook.tears--
		if last, ok := cmds[len(cmds)-1].(*redis.StringCmd); ok {
			last.SetErr(nil)
			last.SetVal(last.Val() + "-next")
		}
	}
	return nil
}

// A read whose revision moved between its first and last GET is a torn
// read and its fields are not used: one torn read is read again and the
// stable second read is the answer; two in a row is ErrRevisionMoved and no
// publication. Each try is one pipeline - GET, MGET, GET - and never a
// MULTI.
func TestATornDistributionReadIsReadAgainOnceAndThenRefused(t *testing.T) {
	server := redistest.Start(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	key := ConfigKey(DefaultKeyPrefix, Tenant, FieldIsAccessBKData.DBKey())
	if err := client.MSet(ctx, key, `true`, RevisionKey(DefaultKeyPrefix), "r1").Err(); err != nil {
		t.Fatal(err)
	}
	hook := &tearingHook{}
	client.AddHook(hook)
	source, err := NewRedisSource(client, DefaultKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}

	publication, err := source.Read(ctx, Fields)
	if err != nil || publication.Retried || len(hook.pipelines) != 1 {
		t.Fatalf("a stable read = %+v, %v after %d tries; want one try, not retried", publication, err, len(hook.pipelines))
	}

	hook.tears, hook.pipelines = 1, nil
	publication, err = source.Read(ctx, Fields)
	if err != nil || !publication.Published || publication.Revision != "r1" || string(publication.Values[FieldIsAccessBKData]) != `true` {
		t.Fatalf("one torn read then a stable one = %+v, %v; want the stable read's publication", publication, err)
	}
	if !publication.Retried {
		t.Fatalf("one torn read then a stable one = %+v; want it to say it was read again", publication)
	}
	if len(hook.pipelines) != 2 {
		t.Fatalf("pipelines %v, want the torn read and one more", hook.pipelines)
	}
	for _, pipeline := range hook.pipelines {
		if strings.Join(pipeline, " ") != "get mget get" {
			t.Fatalf("pipeline %v, want get mget get", pipeline)
		}
	}

	hook.tears, hook.pipelines = 2, nil
	publication, err = source.Read(ctx, Fields)
	if !errors.Is(err, ErrRevisionMoved) || publication.Published || len(publication.Values) != 0 || !publication.Retried {
		t.Fatalf("two torn reads = %+v, %v; want ErrRevisionMoved, nothing, and retried", publication, err)
	}
	if len(hook.pipelines) != 2 {
		t.Fatalf("pipelines %v, want two tries and no third", hook.pipelines)
	}

	// A revision that appears between the two GETs is torn as well: nothing,
	// then something, is not a stable "unpublished".
	if err := client.Del(ctx, RevisionKey(DefaultKeyPrefix)).Err(); err != nil {
		t.Fatal(err)
	}
	hook.tears, hook.pipelines = 1, nil
	if publication, err := source.Read(ctx, Fields); err != nil || publication.Published || !publication.Retried || len(hook.pipelines) != 2 {
		t.Fatalf("a revision appearing mid-read = %+v, %v after %d tries; want it read again, then unpublished", publication, err, len(hook.pipelines))
	}
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	return redistest.FreeAddress(t)
}

func startRedisServer(t *testing.T, _ string, address string) {
	t.Helper()
	redistest.StartAt(t, address)
}

func waitRedisReady(t *testing.T, client *redis.Client) {
	t.Helper()
	// The server answered before StartAt returned; what is left is this
	// client's own connection, under the same bound.
	deadline := time.Now().Add(redistest.ReadyWithin)
	for time.Now().Before(deadline) {
		if err := client.Ping(context.Background()).Err(); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
}
