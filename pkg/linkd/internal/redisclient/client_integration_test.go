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
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSentinelIntegration 仅在显式提供 Sentinel 环境时验证 master 发现和数据节点 PING。
func TestSentinelIntegration(t *testing.T) {
	addressesText := os.Getenv("LINKD_TEST_REDIS_SENTINEL_ADDRESSES")
	if addressesText == "" {
		t.Skip("LINKD_TEST_REDIS_SENTINEL_ADDRESSES is not set")
	}
	masterName := os.Getenv("LINKD_TEST_REDIS_SENTINEL_MASTER_NAME")
	if masterName == "" {
		t.Fatal("LINKD_TEST_REDIS_SENTINEL_MASTER_NAME is required")
	}

	client, err := New(Options{
		Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"),
		Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"),
		Sentinel: &SentinelOptions{
			MasterName: masterName,
			Addresses:  strings.Split(addressesText, ","),
			Username:   os.Getenv("LINKD_TEST_REDIS_SENTINEL_USERNAME"),
			Password:   os.Getenv("LINKD_TEST_REDIS_SENTINEL_PASSWORD"),
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("PING through Sentinel: %v", err)
	}
}
