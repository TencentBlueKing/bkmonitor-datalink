// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"regexp"
)

// OneModelDorisResource 使用 Doris MySQL 查询端口；仅查询 KAC 既有实例和关系表，不创建 schema。
type OneModelDorisResource struct {
	Address       string `yaml:"address" json:"address"`
	Database      string `yaml:"database" json:"database"`
	Username      string `yaml:"username" json:"username"`
	Password      string `yaml:"password" json:"password"`
	InstanceTable string `yaml:"instance_table,omitempty" json:"instance_table,omitempty"`
	EdgeTable     string `yaml:"edge_table,omitempty" json:"edge_table,omitempty"`
}

// Validate 不探测服务，禁止把任意 SQL 放进表名。
func (c OneModelDorisResource) Validate() error {
	if err := (MySQLConfig{Address: c.Address, Database: c.Database, Username: c.Username, Password: c.Password}).Validate(); err != nil {
		return err
	}
	identifier := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)
	for _, table := range []string{c.InstanceTable, c.EdgeTable} {
		if len(table) > 256 || table != "" && !identifier.MatchString(table) {
			return fmt.Errorf("invalid Doris table identifier")
		}
	}
	return nil
}
