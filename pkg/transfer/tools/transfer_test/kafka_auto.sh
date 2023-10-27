#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 自动写入一系列测试数据
./kafka_producer  --address 10.0.0.1:9092 --topic 0bk_monitorv3_10010 --times 50000 --path data/basereport_1001.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bkmonitor_15008700 --times 50000 --path data/exporter_1500870.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bk_monitorv3_10110 --times 1600000 --path data/uptimecheck_1011.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bkmonitor_15005110 --times 1600000 --path data/script_1500511.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bkmonitor_11000040 --times 1600000 --path data/custom_1100004.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bk_monitorv3_10130 --times 1600000 --path data/process_port_1013.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bk_monitorv3_10070 --times 50000 --path data/process_perf_1007.data > /dev/null
./kafka_producer  --address 10.0.0.1:9092 --topic 0bkmonitor_15009590 --times 5000 --path data/bklog_1500959.data > /dev/null