// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

type Policy struct {
	ClusterName string `yaml:"cluster_name" json:"cluster_name"`
	Database    string `yaml:"database" json:"database"`
	TagRouter   string `yaml:"tag_router" json:"tag_router"`
	Enable      bool   `yaml:"enable" json:"enable"`
}

type Shard struct {
	ClusterName     string `yaml:"cluster_name" json:"cluster_name"`
	Database        string `yaml:"database" json:"database"`
	RetentionPolicy string `yaml:"retention_policy" json:"retention_policy"`
	TagRouter       string `yaml:"tag_router" json:"tag_router"`

	Status string `yaml:"status" json:"status"`
}
