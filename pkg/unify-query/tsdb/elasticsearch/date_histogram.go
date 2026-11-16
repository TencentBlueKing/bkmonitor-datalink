// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"

	"github.com/blang/semver"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func hasDateHistogram(aggregates metadata.Aggregates) bool {
	for _, aggregate := range aggregates {
		switch aggregate.Name {
		case DateHistogram:
			return true
		case Max, Min, Avg, Sum, Count, Cardinality, Percentiles:
			if aggregate.Window > 0 && !aggregate.Without {
				return true
			}
		}
	}
	return false
}

func supportsExplicitDateHistogramInterval(version semver.Version) bool {
	// calendar_interval 和 fixed_interval 从 ES 7.2 开始提供。
	return version.Major > 7 || version.Major == 7 && version.Minor >= 2
}

func (i *Instance) configureDateHistogram(ctx context.Context, query *metadata.Query, fact *FormatFactory) error {
	// BKData 通过独立网关提供 ES 协议，根路径不提供集群版本信息。
	if query.SourceType == structured.BkData || !hasDateHistogram(query.Aggregates) {
		return nil
	}

	version, err := i.serverVersion(ctx)
	if err != nil {
		return err
	}
	fact.WithExplicitDateHistogramInterval(supportsExplicitDateHistogramInterval(version))
	return nil
}
