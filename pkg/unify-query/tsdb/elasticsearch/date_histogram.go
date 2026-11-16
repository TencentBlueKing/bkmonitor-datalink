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
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/blang/semver"
	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	dateHistogramSourceRoot       = "root"
	dateHistogramSourceCapability = "capability"
	dateHistogramModeLegacy       = "legacy"
	dateHistogramModeExplicit     = "explicit"
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

func (i *Instance) configureDateHistogram(ctx context.Context, query *metadata.Query, fact *FormatFactory, indexes []string) (err error) {
	// BKData 通过独立网关提供 ES 协议，根路径不提供集群版本信息。
	if query.SourceType == structured.BkData || !hasDateHistogram(query.Aggregates) {
		return nil
	}

	ctx, span := trace.NewSpan(ctx, "elasticsearch-date-histogram")
	source, mode := dateHistogramSourceRoot, elasticsearchUnknown
	span.Set("es-version", elasticsearchUnknown)
	span.Set("es-storage-id", query.StorageID)
	defer func() {
		result := elasticsearchResultSuccess
		if err != nil {
			result = elasticsearchResultError
		}
		span.Set("es-version-source", source)
		span.Set("es-date-histogram-mode", mode)
		metric.ElasticsearchDateHistogramResolutionInc(ctx, source, mode, result)
		span.End(&err)
	}()
	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	version, err := i.serverVersion(ctx)
	var explicit bool
	if err == nil {
		span.Set("es-version", version.String())
		explicit = supportsExplicitDateHistogramInterval(version)
	} else {
		var responseError *elastic.Error
		if !errors.As(err, &responseError) || responseError.Status != http.StatusForbidden {
			return err
		}
		// 根接口与索引读取的权限独立；仅对 403 使用目标索引验证语法能力。
		source = dateHistogramSourceCapability
		explicit, err = i.probeDateHistogramInterval(ctx, indexes, fact.timeField.Name)
		if err != nil {
			return err
		}
	}
	mode = dateHistogramModeLegacy
	if explicit {
		mode = dateHistogramModeExplicit
	}
	fact.WithExplicitDateHistogramInterval(explicit)
	return nil
}

func (i *Instance) probeDateHistogramInterval(ctx context.Context, indexes []string, field string) (explicit bool, err error) {
	ctx, span := trace.NewSpan(ctx, "elasticsearch-date-histogram-capability")
	defer span.End(&err)
	// 空目标不能变成全索引查询。
	if len(indexes) == 0 {
		return false, fmt.Errorf("cannot probe date histogram interval without target indexes")
	}
	client, err := i.getClient(ctx, i.connect)
	if err != nil {
		return false, err
	}
	defer client.Stop()
	// match_none 不读取文档；静态间隔只检查 fixed/calendar 共同引入的语法能力。
	result, err := client.Search(indexes...).Size(0).Query(elastic.NewMatchNoneQuery()).
		Aggregation("_uq_interval", elastic.NewDateHistogramAggregation().Field(field).FixedInterval("1s")).Do(ctx)
	if err != nil {
		var responseError *elastic.Error
		if errors.As(err, &responseError) {
			span.Set("http-status", responseError.Status)
		}
		if unsupportedExplicitDateHistogramInterval(err) {
			span.Set("es-explicit-interval", false)
			return false, nil
		}
		return false, fmt.Errorf("probe Elasticsearch date histogram interval: %w", err)
	}
	span.Set("http-status", http.StatusOK)
	if result.Shards == nil || result.Error != nil || result.Status >= http.StatusBadRequest || result.TimedOut || hasFailedSearchShards(result) {
		return false, fmt.Errorf("incomplete Elasticsearch date histogram capability response")
	}
	span.Set("es-explicit-interval", true)
	return true, nil
}

func unsupportedExplicitDateHistogramInterval(err error) bool {
	var responseError *elastic.Error
	if !errors.As(err, &responseError) || responseError.Status != http.StatusBadRequest || responseError.Details == nil {
		return false
	}
	// 仅识别当前静态检查中 fixed_interval 的未知字段错误，其他 400 保持失败。
	for _, details := range append([]*elastic.ErrorDetails{responseError.Details}, responseError.Details.RootCause...) {
		if details != nil && (details.Type == "illegal_argument_exception" || details.Type == "x_content_parse_exception") &&
			strings.Contains(details.Reason, "[date_histogram]") && strings.Contains(details.Reason, "unknown field [fixed_interval]") {
			return true
		}
	}
	return false
}
