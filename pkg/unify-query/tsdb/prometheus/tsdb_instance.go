// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	baseInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	tsDBService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func GetTsDbInstance(ctx context.Context, qry *metadata.Query) tsdb.Instance {
	var (
		instance tsdb.Instance
		err      error
		user     = metadata.GetUser(ctx)
	)

	ctx, span := trace.NewSpan(ctx, "get-ts-db-instance")
	defer func() {
		if err != nil {
			log.Errorf(ctx, "get_ts_db_instance tableID: %s error: %s", qry.TableID, err.Error())
		}
		span.End(&err)
	}()

	span.Set("storage-id", qry.StorageID)

	span.Set("storage-type", qry.StorageType)
	curlGet := &curl.HttpCurl{Log: log.DefaultLogger}

	switch qry.StorageType {
	case consul.InfluxDBStorageType:
		opt := &influxdb.Options{
			Timeout:        tsDBService.InfluxDBTimeout,
			ContentType:    tsDBService.InfluxDBContentType,
			ChunkSize:      tsDBService.InfluxDBChunkSize,
			RawUriPath:     tsDBService.InfluxDBQueryRawUriPath,
			Accept:         tsDBService.InfluxDBQueryRawAccept,
			AcceptEncoding: tsDBService.InfluxDBQueryRawAcceptEncoding,
			MaxLimit:       tsDBService.InfluxDBMaxLimit,
			MaxSlimit:      tsDBService.InfluxDBMaxSLimit,
			Tolerance:      tsDBService.InfluxDBTolerance,
			ReadRateLimit:  tsDBService.InfluxDBQueryReadRateLimit,
			Curl:           curlGet,
		}
		var host *routerInfluxdb.Host
		host, err = baseInfluxdb.GetInfluxDBRouter().GetInfluxDBHost(
			ctx, qry.TagsKey, qry.ClusterName, qry.DB, qry.Measurement, qry.Condition,
		)
		if err != nil {
			return nil
		}
		opt.Host = host.DomainName
		opt.Port = host.Port
		opt.GrpcPort = host.GrpcPort
		opt.Protocol = host.Protocol
		opt.Username = host.Username
		opt.Password = host.Password
		// 如果 host 有单独配置，则替换默认限速配置
		if host.ReadRateLimit > 0 {
			opt.ReadRateLimit = host.ReadRateLimit
		}

		span.Set("cluster-name", qry.ClusterName)
		span.Set("tag-keys", qry.TagsKey)
		span.Set("ins-option", opt)

		instance, err = influxdb.NewInstance(ctx, opt)
	case consul.ElasticsearchStorageType:
		opt := &elasticsearch.InstanceOption{
			MaxSize:    tsDBService.EsMaxSize,
			Timeout:    tsDBService.EsTimeout,
			MaxRouting: tsDBService.EsMaxRouting,
		}

		if qry.SourceType == structured.BkData {
			opt.Connects = append(opt.Connects, elasticsearch.Connect{Address: bkapi.GetBkDataAPI().QueryUrlForES(user.SpaceUID)})
			opt.Headers = bkapi.GetBkDataAPI().Headers(nil)
			opt.HealthCheck = false
		} else {
			storages := qry.StorageIDs
			if len(storages) == 0 {
				storages = []string{qry.StorageID}
			}

			for _, sid := range storages {
				stg, _ := tsdb.GetStorage(sid)
				if stg == nil {
					err = fmt.Errorf("%s storage list is empty in %s", consul.ElasticsearchStorageType, qry.StorageID)
					continue
				}

				opt.Connects = append(opt.Connects, elasticsearch.Connect{
					Address:  stg.Address,
					UserName: stg.Username,
					Password: stg.Password,
				})
			}
			opt.HealthCheck = true
		}
		instance, err = elasticsearch.NewInstance(ctx, opt)
	case consul.BkSqlStorageType:
		instance, err = bksql.NewInstance(ctx, &bksql.Options{
			Address: bkapi.GetBkDataAPI().QueryUrl(user.SpaceUID),
			Headers: bkapi.GetBkDataAPI().Headers(map[string]string{
				bksql.ContentType: tsDBService.BkSqlContentType,
			}),
			Timeout:    tsDBService.BkSqlTimeout,
			MaxLimit:   tsDBService.BkSqlLimit,
			Tolerance:  tsDBService.BkSqlTolerance,
			SliceLimit: qry.Size,
			Curl:       curlGet,
		})
	case consul.VictoriaMetricsStorageType:
		instance, err = victoriaMetrics.NewInstance(ctx, &victoriaMetrics.Options{
			Address: bkapi.GetBkDataAPI().QueryUrl(user.SpaceUID),
			Headers: bkapi.GetBkDataAPI().Headers(map[string]string{
				victoriaMetrics.ContentType: tsDBService.VmContentType,
			}),
			MaxConditionNum:  tsDBService.VmMaxConditionNum,
			Timeout:          tsDBService.VmTimeout,
			InfluxCompatible: tsDBService.VmInfluxCompatible,
			UseNativeOr:      tsDBService.VmUseNativeOr,
			Curl:             curlGet,
			ForceStorageName: tsDBService.QueryRouterForceVmClusterName,
		})
		span.Set("vm-force-storage-name", tsDBService.QueryRouterForceVmClusterName)
	default:
		err = fmt.Errorf("storage type is error %+v", qry)
	}

	return instance
}
