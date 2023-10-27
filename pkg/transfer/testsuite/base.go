// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testsuite

import (
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/conv"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem/processor"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/influxdb"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/redis"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/auto"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/basereport"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/exporter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flat"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/procperf"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/procport"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
)

//go:generate mockgen -package=${GOPACKAGE} -destination=mock_define.go transfer/define Pipeline,DataProcessor,Payload,Frontend,Backend,Store,Task,Service,Session,ServiceWatcher
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_etl.go transfer/etl Container,Field,Record,Schema
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_pipeline.go transfer/pipeline Connector,Node
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_consul.go transfer/consul SourceClient
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_cluster_frontend.go -mock_names Client=MockKafkaClusterClient,Consumer=MockKafkaClusterConsumer,OffsetManager=MockKafkaOffsetManager transfer/kafka Client,Consumer,OffsetManager
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_influx_client.go -mock_names Client=MockInfluxDBClient transfer/influxdb Client
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_sarama.go github.com/Shopify/sarama Client,ConsumerGroup,ConsumerGroupSession,ConsumerGroupClaim
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_sling.go github.com/dghubble/sling Doer
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_sarama_producer.go transfer/kafka Producer
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_redis_client.go transfer/redis ClientOfRedis
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_redis_pipeline.go github.com/go-redis/redis Pipeliner
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_es_writer.go transfer/elasticsearch BulkWriter
//go:generate mockgen -package=${GOPACKAGE} -destination=mock_utils.go transfer/utils Semaphore
