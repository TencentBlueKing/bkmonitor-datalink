// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

// RtDetail holds the mapping information for a single vm_rt entry.
// It is used by spanSetStorageListDiff to provide context when reporting
// storage-list mismatches: vm_rt is the observation subject while StorageName
// (cluster name) is the comparison subject.
type RtDetail struct {
	TableID     string `json:"table_id"`
	StorageName string `json:"storage_name"`
}

type VmExpand struct {
	ResultTableList       []string
	MetricFilterCondition map[string]string
	ClusterName           string

	// RtDetailList maps vm_rt → RtDetail{TableID, StorageName}.
	// Populated alongside ResultTableList so that spanSetStorageListDiff can
	// use StorageName as the comparison subject and include TableID as an
	// explanatory field.
	RtDetailList map[string]RtDetail
}
