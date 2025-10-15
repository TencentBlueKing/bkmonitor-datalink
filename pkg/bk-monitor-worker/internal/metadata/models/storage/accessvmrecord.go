// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in accessvmrecord.go -out qs_accessvmrecord_gen.go

// AccessVMRecord access vm record model
// gen:qs
type AccessVMRecord struct {
	BkTenantId       string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	DataType         string `json:"data_type" gorm:"size:32"`
	ResultTableId    string `gorm:"result_table_id;size:64" json:"result_table_id"`
	BcsClusterId     string `gorm:"bcs_cluster_id;size:32" json:"bcs_cluster_id"`
	StorageClusterID uint   `gorm:"storage_cluster_id" json:"storage_cluster_id"`
	VmClusterId      uint   `gorm:"vm_cluster_id" json:"vm_cluster_id"`
	BkBaseDataId     uint   `gorm:"bk_base_data_id" json:"bk_base_data_id"`
	BkBaseDataName   string `gorm:"bk_base_data_name;size:64" json:"bk_base_data_name"`
	VmResultTableId  string `gorm:"vm_result_table_id;size:64" json:"vm_result_table_id"`
	Remark           string `gorm:"size:256" json:"remark"`
}

// TableName 用于设置表的别名
func (AccessVMRecord) TableName() string {
	return "metadata_accessvmrecord"
}
