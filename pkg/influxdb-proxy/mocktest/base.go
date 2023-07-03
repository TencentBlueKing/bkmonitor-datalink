// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mocktest

//go:generate mockgen -package=mocktest -destination=mock_backend.go  -mock_names Backend=ProxyBackend github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend Backend
//go:generate mockgen -package=mocktest -destination=mock_kafka_v2.go  -mock_names Backend=MockBackupStorage github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb StorageBackup
//go:generate mockgen -package=mocktest -destination mock_cluster.go -mock_names Cluster=MockCluster github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster Cluster
//go:generate mockgen -package=mocktest -destination=mock_httpclient.go -mock_names Client=InfluxClient github.com/influxdata/influxdb/client/v2 Client,BatchPoints
//go:generate mockgen -package=mocktest -destination=mock_sarama.go -mock_names Client=KafkaClient github.com/Shopify/sarama Client,ConsumerGroup,ConsumerGroupSession,ConsumerGroupClaim,SyncProducer,OffsetManager,PartitionOffsetManager,ClusterAdmin
//go:generate mockgen -package=mocktest -destination=mock_sarama_broker.go  github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb Broker
//go:generate mockgen -package=mocktest -destination=http_client.go  -mock_names Client=HTTPClient github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb Client
//go:generate mockgen -package=mocktest -destination=mock_consul.go  -mock_names KV=MockAbstractKV,Plan=MockAbstractPlan,Agent=MockAbstractAgent github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul/base ConsulClient,KV,Plan,Agent,Session
