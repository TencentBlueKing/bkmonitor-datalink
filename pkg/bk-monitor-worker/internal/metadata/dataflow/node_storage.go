// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// StorageNode 存储节点
type StorageNode struct {
	SourceRtId     string
	BkBizId        int
	ProcessRtId    string
	StorageExpires int
	BaseNode
}

func NewStorageNode(sourceRtId string, storageExpires int, parentList []Node) *StorageNode {
	n := &StorageNode{BaseNode: *NewBaseNode(parentList)}
	n.SourceRtId = sourceRtId
	n.SourceRtId = sourceRtId
	splitStr := strings.SplitN(sourceRtId, "_", 2)
	if len(splitStr) != 2 {
		return nil
	} else {
		bizStr := splitStr[0]
		bizId, err := strconv.Atoi(bizStr)
		if err != nil {
			logger.Errorf("parse bkBizId from sourceRtId [%s] failed", sourceRtId)
			return nil
		}
		n.BkBizId = bizId
		n.ProcessRtId = splitStr[1]
	}
	if storageExpires < 0 || storageExpires > config.GlobalBkdataDataExpiresDays {
		n.StorageExpires = config.GlobalBkdataDataExpiresDays
	} else {
		n.StorageExpires = storageExpires
	}

	return n
}

func (n StorageNode) Equal(other map[string]interface{}) bool {
	c := n.Config()
	if equal, _ := jsonx.CompareObjects(c["from_result_table_ids"], other["from_result_table_ids"]); equal {
		if equal, _ := jsonx.CompareObjects(c["table_name"], other["table_name"]); equal {
			if equal, _ := jsonx.CompareObjects(c["bk_biz_id"], other["bk_biz_id"]); equal {
				if equal, _ := jsonx.CompareObjects(c["cluster"], other["cluster"]); equal {
					return true
				}
			}
		}
	}
	return false
}

// Name 节点名
func (n StorageNode) Name() string {
	return fmt.Sprintf("%s(%s)", n.GetNodeType(), n.SourceRtId)
}

// OutputTableName 输出表名（带上业务ID前缀）
func (n StorageNode) OutputTableName() string {
	return n.SourceRtId
}

// DruidStorageNode druid存储节点
type DruidStorageNode struct {
	StorageNode
}

func NewDruidStorageNode(sourceRtId string, storageExpires int, parentList []Node) *DruidStorageNode {
	n := &DruidStorageNode{StorageNode: *NewStorageNode(sourceRtId, storageExpires, parentList)}
	n.NodeType = "druid_storage"
	return n
}

func (n DruidStorageNode) Config() map[string]interface{} {
	return map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId},
		"bk_biz_id":             n.BkBizId,
		"result_table_id":       n.SourceRtId,
		"name":                  n.Name(),
		"expires":               n.StorageExpires,
		"cluster":               config.GlobalBkdataDruidStorageClusterName,
	}
}

// TSpiderStorageNode tspider存储节点
type TSpiderStorageNode struct {
	StorageNode
}

func NewTSpiderStorageNode(sourceRtId string, storageExpires int, parentList []Node) *TSpiderStorageNode {
	n := &TSpiderStorageNode{StorageNode: *NewStorageNode(sourceRtId, storageExpires, parentList)}
	n.NodeType = "tspider_storage"
	return n
}

func (n TSpiderStorageNode) Config() map[string]interface{} {
	return map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId},
		"bk_biz_id":             n.BkBizId,
		"result_table_id":       n.SourceRtId,
		"name":                  n.Name(),
		"expires":               n.StorageExpires,
		"cluster":               config.GlobalBkdataMysqlStorageClusterName,
	}
}

func CreateTSpiderOrDruidNode(sourceRtId string, storageExpires int, parentList []Node) Node {
	isSystemRt := strings.HasPrefix(sourceRtId, fmt.Sprintf("%s_%s_system_", config.GlobalBkdataBkBizId, config.GlobalBkdataRtIdPrefix))
	if config.GlobalBkdataDruidStorageClusterName != "" && isSystemRt {
		return NewDruidStorageNode(sourceRtId, storageExpires, parentList)
	} else {
		return NewTSpiderStorageNode(sourceRtId, storageExpires, parentList)
	}
}
