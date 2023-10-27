#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

influx -database db1 -execute "create database db1" -port 8086
influx -database db1 -execute "create database db1" -port 8087
influx -database db1 -execute "create database db1" -port 8088
influx -database db1 -execute "create database db1" -port 8089
influx -database db1 -execute "create database db1" -port 8090
influx -database db1 -execute "drop measurement table1" -port 8086
influx -database db1 -execute "drop measurement table1" -port 8087
influx -database db1 -execute "drop measurement table1" -port 8088
influx -database db1 -execute "drop measurement table1" -port 8089
influx -database db1 -execute "drop measurement table1" -port 8090