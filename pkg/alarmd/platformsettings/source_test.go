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
	"bytes"
	"context"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// The Redis source against a real server, on the protocol's own keys and
// in its own shape (one MULTI: GET revision, MGET fields). Nothing
// published is Published false with no error; an absent field is absent; a
// present field comes back as the raw JSON the platform wrote, null
// included, so that "no override" and "present and empty" stay apart.
func TestRedisSourceReadsTheProtocolKeysInOneTransaction(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
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

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func startRedisServer(t *testing.T, executable, address string) {
	t.Helper()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable,
		"--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning",
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("redis-server start error = %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
}

func waitRedisReady(t *testing.T, client *redis.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(context.Background()).Err(); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
}
