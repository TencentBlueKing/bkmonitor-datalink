// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
)

type mockAction struct {
}

func (m *mockAction) Move(ctx context.Context, s *shard.Shard) error {
	return nil
}

func (m *mockAction) Clean(ctx context.Context, s *shard.Shard) error {
	return nil
}

func (m *mockAction) Rebuild(ctx context.Context, shard *shard.Shard) error {
	return nil
}

func TestPolicy(t *testing.T) {
	dir, _ := os.Getwd()
	sourceDir := fmt.Sprintf("%s/source", dir)

	clusterName := "default"

	database := "db"
	tagRouter := ""

	targetName := "target_name"
	targetDir := fmt.Sprintf("%s/target", dir)

	instanceName, _ := os.Hostname()
	address := ""
	username := ""
	password := ""

	ctx := context.TODO()
	logger := log.NewLogger()

	policyMeta := &Meta{
		ClusterName: clusterName,
		Database:    database,
		TagRouter:   tagRouter,
	}

	store := stores.NewInfluxDB(
		logger, clusterName, instanceName, tagRouter,
		sourceDir, targetName, targetDir, address, username, password,
	)

	policy := NewPolicy(
		ctx, policyMeta, store,
		nil, logger,
	)

	shards := policy.GetActiveShards(ctx, nil)
	for _, s := range shards {
		err := s.Run(ctx, new(mockAction), nil, nil, nil)
		assert.Nil(t, err)
	}

	for _, d := range shards {
		assert.Equal(t, shard.Rebuild, d.Status.Code)
	}
}
