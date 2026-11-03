// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"testing"
)

// The fingerprint key is made by the first process to ask and read by every
// other, so replicas and restarts fingerprint a value alike; without a store
// there is none, and the provider makes a process key of its own.
func TestTheFingerprintKeyIsOnePerDeployment(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	first := sreFingerprintKey(client, "test.sre:{env}:fingerprint-key")
	second := sreFingerprintKey(client, "test.sre:{env}:fingerprint-key")
	if len(first) != 32 || !bytes.Equal(first, second) {
		t.Fatalf("keys %x and %x, want one 32-byte key", first, second)
	}
	if other := sreFingerprintKey(client, "test.sre:{other}:fingerprint-key"); bytes.Equal(other, first) {
		t.Fatal("another deployment's key is the same")
	}
	if ttl := client.TTL(context.Background(), "test.sre:{env}:fingerprint-key").Val(); ttl >= 0 {
		t.Fatalf("the key expires (%v); a new key would change every fingerprint", ttl)
	}
	if sreFingerprintKey(nil, "k") != nil {
		t.Fatal("a key without a store")
	}
}
