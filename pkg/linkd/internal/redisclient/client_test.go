// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisclient

import (
	"reflect"
	"strings"
	"testing"
)

func TestNewStandaloneClient(t *testing.T) {
	t.Parallel()

	client, err := New(Options{
		Address: "redis.example.com:6379", Username: "linkd", Password: "secret", Database: 2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	got := client.Options()
	if got.Addr != "redis.example.com:6379" || got.Username != "linkd" || got.Password != "secret" || got.DB != 2 {
		t.Fatalf("New() options = %#v", got)
	}
}

func TestNewSentinelClient(t *testing.T) {
	t.Parallel()

	client, err := New(Options{
		Username: "redis-user", Password: "redis-secret", Database: 3,
		Sentinel: &SentinelOptions{
			MasterName: "linkd-master",
			Addresses:  []string{"sentinel-a.example.com:26379", "sentinel-b.example.com:26379"},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	got := client.Options()
	if got.Addr != "FailoverClient" || got.Username != "redis-user" || got.Password != "redis-secret" || got.DB != 3 {
		t.Fatalf("New() failover options = %#v", got)
	}
}

func TestNewFailoverOptionsSeparatesSentinelAndDataNodeCredentials(t *testing.T) {
	t.Parallel()

	options := Options{
		Username: "redis-user", Password: "redis-secret", Database: 3,
		Sentinel: &SentinelOptions{
			MasterName: "linkd-master",
			Addresses:  []string{"sentinel-a.example.com:26379", "sentinel-b.example.com:26379"},
			Username:   "sentinel-user",
			Password:   "sentinel-secret",
		},
	}
	got := newFailoverOptions(options)
	if got.MasterName != "linkd-master" ||
		!reflect.DeepEqual(got.SentinelAddrs, options.Sentinel.Addresses) ||
		got.SentinelUsername != "sentinel-user" || got.SentinelPassword != "sentinel-secret" ||
		got.Username != "redis-user" || got.Password != "redis-secret" || got.DB != 3 {
		t.Fatalf("newFailoverOptions() = %#v", got)
	}
	got.SentinelAddrs[0] = "changed:26379"
	if options.Sentinel.Addresses[0] != "sentinel-a.example.com:26379" {
		t.Fatal("newFailoverOptions() shares SentinelAddrs with input")
	}
}

func TestOptionsValidateRejectsInvalidSentinel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		options   Options
		wantError string
	}{
		{name: "missing master", options: Options{Sentinel: &SentinelOptions{Addresses: []string{"sentinel:26379"}}}, wantError: "master_name"},
		{name: "missing addresses", options: Options{Sentinel: &SentinelOptions{MasterName: "master"}}, wantError: "addresses must not be empty"},
		{name: "invalid address", options: Options{Sentinel: &SentinelOptions{MasterName: "master", Addresses: []string{"sentinel"}}}, wantError: "must be host:port"},
		{name: "duplicate address", options: Options{Sentinel: &SentinelOptions{MasterName: "master", Addresses: []string{"sentinel:26379", "sentinel:26379"}}}, wantError: "duplicates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.options.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
