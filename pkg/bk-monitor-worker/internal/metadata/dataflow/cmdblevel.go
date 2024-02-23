// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

type CMDBPrepareAggregateTask struct {
	BaseTask
	RtId            string
	AggInterval     int
	AggMethod       string
	MetricField     []string
	DimensionFields []string
}

func NewCMDBPrepareAggregateTask(rtId string, aggInterval int, aggMethod string, metricField []string, dimensionFields []string) *CMDBPrepareAggregateTask {
	return &CMDBPrepareAggregateTask{RtId: rtId, AggInterval: aggInterval, AggMethod: aggMethod, MetricField: metricField, DimensionFields: dimensionFields}
}

func (f CMDBPrepareAggregateTask) FlowName() string {
	//TODO implement me
	panic("implement me")
}

func (f CMDBPrepareAggregateTask) CreateFlow(rebuild bool, projectId int) error {
	//TODO implement me
	panic("implement me")
}

func (f CMDBPrepareAggregateTask) StartFlow(consumingMode string) error {
	//TODO implement me
	panic("implement me")
}
