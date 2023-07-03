// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"strconv"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type CmdbEventSender struct {
	cmdbLastUpdateTime time.Time       // cmdb信息的最后更新时间
	cmdbLevelInfo      []common.MapStr // CMDB层级信息缓存
}

// DuplicateRecordByCMDBLevel 根据下发的信息，将一条数据复制为多条返回
func (s *CmdbEventSender) DuplicateRecordByCMDBLevel(m common.MapStr, labels []configs.Label) []common.MapStr {
	var (
		finalResult  = make([]common.MapStr, 0)
		roundMap     common.MapStr
		dimensionMap map[string]interface{}
		ok           bool
		isAppend     = false
	)

	// 1. 判断是否有更新的cmdb_level信息
	if s.cmdbLastUpdateTime != define.GlobalWatcher.GetUpdateTime() {
		// 如果有更新，则需要获取新的cmdb_level信息结果
		s.updateCMDBLevelInfo(labels)
		logger.Debugf("update cmdbLevel success, now has cmdb_level->[%d], cmdb_level:%v", len(s.cmdbLevelInfo), s.cmdbLevelInfo)
	}

	// 2. 遍历所有的层级，逐一的将内容追加到维度中
	for _, cmdbLevelInfo := range s.cmdbLevelInfo {

		// 每次都需要复制得到一个新的map
		roundMap = m.Clone()

		// 先提前将dimension转换为目标类型
		if dimensionMap, ok = roundMap["dimension"].(common.MapStr); !ok {
			logger.Warnf("failed to convert dimension to map[string]interface{} content->[%v], "+
				"maybe something go wrong?", roundMap["dimension"])
			continue
		}

		// 每一个CMDB的层级链路，都需要逐一拆解并注入到结构当中
		for cmdbLevelName, cmdbLevelID := range cmdbLevelInfo {
			dimensionMap[cmdbLevelName] = cmdbLevelID
		}
		finalResult = append(finalResult, roundMap)
		isAppend = true
	}

	// 判断是否没有任何一条记录命中，如果是，则需要将原始数据放入，确保有一条数据上报
	if !isAppend {
		finalResult = []common.MapStr{m}
		//logger.Info("no cmdb level is match, original message will report.")
	}

	return finalResult
}

/*
	getCMDBLevelInfo: 根据label配置，获取所有命中cmdb层级信息

补充逻辑：

  - 按主机下发，比如（主机-操作系统）

  - 目标选择静态主机，补充：bk_target_ip, bk_target_cloud_id, bk_xxxx_id（xxx为topo节点）

  - 目标选择动态节点，补充：bk_target_ip, bk_target_cloud_id, bk_xxxx_id（xxx为topo节点）

  - 按实例下发，比如（服务-组件）

  - 目标选择动态节点，补充：bk_target_service_instance_id, bk_target_ip, bk_target_cloud_id, bk_xxxx_id（xxx为topo节点）
*/
func (s *CmdbEventSender) updateCMDBLevelInfo(labels []configs.Label) {
	defer func() {
		// 更新后，需要更新最后更新时间
		s.cmdbLastUpdateTime = define.GlobalWatcher.GetUpdateTime()
		logger.Debugf("update cmdbLevel success, now updateTime is->[%d]", s.cmdbLastUpdateTime.Unix())
	}()
	var (
		result, tmpResult []common.MapStr
		topoLinkInfo      host.Info
		roundLevelID      int
		err               error
	)

	// 遍历所有的label内容
	for _, labelInfo := range labels {
		// 1）：如果是label中包含bk_target_service_instance_id，则为实例下发
		if labelInfo.IsServiceInstance() {
			// 按实例节点来补充cmdb信息
			logger.Debugf("fill cmdb level info by service instance topo node")
			roundLevelID, err = strconv.Atoi(labelInfo.BkTargetTopoID)
			if err != nil {
				continue
			}
			// 将label层级命中的信息cmdb链条，都需要获取到
			topoLinkInfo, err = define.GlobalWatcher.GetInfoByLevelID(labelInfo.BkTargetTopoLevel, roundLevelID)
			if err != nil {
				logger.Errorf("failed to get level->[%s] id->[%d] for->[%s], continue next round",
					labelInfo.BkTargetTopoLevel, roundLevelID, err)
				continue
			}
			tmpResult = s.parseTopoLinkInfoToMapStr(
				topoLinkInfo,
				labelInfo.AsMapStr())
		} else if labelInfo.IsHostDynamicTopoNode() {
			// 2）如果不是实例下发，label中又包含CMDB的topo节点信息，且则为按主机，且为动态节点下发
			// 按主机节点来补充cmdb信息
			logger.Debugf("fill cmdb level info by host topo node")
			roundLevelID, err = strconv.Atoi(labelInfo.BkTargetTopoID)
			if err != nil {
				logger.Warnf("failed to convert bkTopoID->[%s] to int for->[%s]", labelInfo.BkTargetTopoID, err)
				continue
			}
			// 将label层级命中的信息cmdb链条，都需要获取到
			topoLinkInfo, err = define.GlobalWatcher.GetInfoByLevelID(labelInfo.BkTargetTopoLevel, roundLevelID)
			if err != nil {
				logger.Errorf("failed to get level->[%s] id->[%d] for->[%s], continue next round",
					labelInfo.BkTargetTopoLevel, roundLevelID, err)
				continue
			}
			tmpResult = s.parseTopoLinkInfoToMapStr(topoLinkInfo, labelInfo.AsMapStr())
		} else if labelInfo.IsHostStaticIp() {
			// 3) 如果label中包含ip信息，则为按主机，且为静态IP下发
			logger.Debugf("fill cmdb level info by ip")
			topoLinkInfo, err = define.GlobalWatcher.GetInfoByCloudIdAndIp(labelInfo.BkTargetCloudID, labelInfo.BkTargetIP)
			if err != nil {
				logger.Errorf("failed to get bk_cloud_id->[%s] bk_host_inner_ip->[%s] for->[%s], continue next round",
					labelInfo.BkTargetCloudID, labelInfo.BkTargetIP, err)
				continue
			}
			tmpResult = s.parseTopoLinkInfoToMapStr(topoLinkInfo, labelInfo.AsMapStr())
		} else {
			logger.Warnf("failed to get cmdb level info, because label info is empty, label info:%v", labelInfo)
			continue
		}
		result = append(result, tmpResult...)
	}

	s.cmdbLevelInfo = result
}

// parseTopoLinkInfoToMapStr: 用来解析原始的topolink信息到维度信息(主要是将value转成字符串)
// @param: topoLinkInfo  host.Info      原始topolink信息
// @param: kwargs        common.MapStr  转成mapstr后，需要额外添加的信息
func (s *CmdbEventSender) parseTopoLinkInfoToMapStr(topoLinkInfo host.Info, kwargs common.MapStr) []common.MapStr {
	result := make([]common.MapStr, 0, len(topoLinkInfo))
	for _, topoLink := range topoLinkInfo {
		tempResult := make(common.MapStr)
		if kwargs != nil {
			tempResult.Update(kwargs)
		}
		// 需要遍历里面所有的cmdb层级信息，然后将自定义层级加上bk_开头和_id结尾
		for key, value := range topoLink {
			if key == host.BkHostInnerIPKey {
				tempResult["bk_target_ip"] = value
			} else if key == host.BkCloudIDKey {
				tempResult["bk_target_cloud_id"] = value
			} else {
				// 所有上报的维度，最后都应该是字符串形式的
				strValue := strconv.Itoa(int(value.(int64)))

				if strings.HasPrefix(key, "bk_") {
					tempResult[key] = strValue
					logger.Debugf("key->[%s] meet starts with bk_, nothing will change.", key)
				} else {
					tempResult["bk_"+key+"_id"] = strValue
					logger.Debugf("key->[%s] is not starts with bk_, will change it.", key)
				}
			}
		}
		result = append(result, tempResult)
	}
	return result
}
