// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/require"

	clusterpkg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

func TestRefreshRoutesKeepsPreviousMapOnMissingCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	stubs := gostub.New()
	defer stubs.Reset()

	previousCluster := NewMockCluster(ctrl)
	stubs.StubFunc(&clusterpkg.GetCluster, nil, clusterpkg.ErrClusterNotExist)

	manager := &Manager{
		ctx: context.Background(),
		routeMap: map[string]clusterpkg.Cluster{
			"db.old": previousCluster,
		},
		tagMap: map[string][]string{},
		usingRouteInfo: map[string]*Info{
			"db.old": {Cluster: "old"},
		},
	}

	err := manager.Refresh(map[string]*Info{
		"db.new": {Cluster: "missing"},
	})
	require.ErrorContains(t, err, "route refresh canceled")

	got, err := manager.GetClusterByRoute(0, "db.old")
	require.NoError(t, err)
	require.Same(t, previousCluster, got)

	err = manager.Refresh(map[string]*Info{})
	require.ErrorContains(t, err, "received empty route data")

	got, err = manager.GetClusterByRoute(0, "db.old")
	require.NoError(t, err)
	require.Same(t, previousCluster, got)
}
