// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// HostFields 主机字段
var HostFields = []string{
	"bk_host_innerip",
	"bk_host_innerip_v6",
	"bk_cloud_id",
	"bk_host_id",
	"bk_agent_id",
	"bk_host_outerip",
	"bk_host_outerip_v6",
	"bk_host_name",
	"bk_os_name",
	"bk_os_type",
	"operator",
	"bk_bak_operator",
	"bk_state_name",
	"bk_isp_name",
	"bk_province_name",
	"bk_supplier_account",
	"bk_state",
	"service_template_id",
	"srv_status",
	"bk_comment",
	"idc_unit_name",
	"net_device_id",
	"rack_id",
	"bk_svr_device_cls_name",
	"svr_device_class",
	"docker_client_version",
	"docker_server_version",
	"bk_mem",
	"bk_disk",
	"bk_os_bit",
	"bk_os_version",
	"bk_cpu_module",
	"bk_cpu",
}

// AlarmHostInfo 告警主机信息
type AlarmHostInfo struct {
	// 原生字段
	BkBizId             int      `json:"bk_biz_id"`
	BkAgentId           string   `json:"bk_agent_id"`
	Operator            []string `json:"operator"`
	BkBakOperator       []string `json:"bk_bak_operator"`
	BkCloudId           int      `json:"bk_cloud_id"`
	BkComment           string   `json:"bk_comment"`
	BkHostId            int      `json:"bk_host_id"`
	BkHostInnerip       string   `json:"bk_host_innerip"`
	BkHostInneripV6     string   `json:"bk_host_innerip_v6"`
	BkHostName          string   `json:"bk_host_name"`
	BkHostOuterip       string   `json:"bk_host_outerip"`
	BkHostOuteripV6     string   `json:"bk_host_outerip_v6"`
	BkOsName            string   `json:"bk_os_name"`
	BkOsType            string   `json:"bk_os_type"`
	BkOsVersion         string   `json:"bk_os_version"`
	BkOsBit             string   `json:"bk_os_bit"`
	BkProvinceName      string   `json:"bk_province_name"`
	BkState             string   `json:"bk_state"`
	BkStateName         string   `json:"bk_state_name"`
	BkIspName           string   `json:"bk_isp_name"`
	BkSupplierAccount   string   `json:"bk_supplier_account"`
	BkMem               *int     `json:"bk_mem"`
	BkDisk              *int     `json:"bk_disk"`
	BkCpu               *int     `json:"bk_cpu"`
	BkCpuModule         string   `json:"bk_cpu_module"`
	ServiceTemplateId   string   `json:"service_template_id"`
	SrvStatus           string   `json:"srv_status"`
	IdcUnitName         string   `json:"idc_unit_name"`
	NetDeviceId         string   `json:"net_device_id"`
	RackId              string   `json:"rack_id"`
	BkSvrDeviceClsName  string   `json:"bk_svr_device_cls_name"`
	SvrDeviceClass      string   `json:"svr_device_class"`
	DockerClientVersion string   `json:"docker_client_version"`
	DockerServerVersion string   `json:"docker_server_version"`

	// 补充字段
	IP          string `json:"ip"`
	BkSetIds    []int  `json:"bk_set_ids"`
	BkModuleIds []int  `json:"bk_module_ids"`
	BkCloudName string `json:"bk_cloud_name"`
	DisplayName string `json:"display_name"`

	TopoLinks [][]string `json:"topo_links"`
}

// NewAlarmHostInfoByListBizHostsTopoDataInfo 通过ListBizHostsTopoDataInfo构造AlarmHostInfo
func NewAlarmHostInfoByListBizHostsTopoDataInfo(info *cmdb.ListBizHostsTopoDataInfo) *AlarmHostInfo {
	// 主备负责人处理
	var operator []string
	var bkBakOperator []string
	if info.Host.Operator == "" {
		operator = []string{}
	} else {
		operator = strings.Split(info.Host.Operator, ",")
	}
	if info.Host.BkBakOperator == "" {
		bkBakOperator = []string{}
	} else {
		bkBakOperator = strings.Split(info.Host.BkBakOperator, ",")
	}

	// 集群/模块ID列表
	var bkSetIds []int
	var bkModuleIds []int
	for _, topo := range info.Topo {
		bkSetIds = append(bkSetIds, topo.BkSetId)
		for _, module := range topo.Module {
			bkModuleIds = append(bkModuleIds, module.BkModuleId)
		}
	}

	// 展示字段处理
	var displayName string
	if info.Host.BkHostInnerip != "" {
		displayName = info.Host.BkHostInnerip
	} else if info.Host.BkHostName != "" {
		displayName = info.Host.BkHostName
	} else if info.Host.BkHostInneripV6 != "" {
		displayName = info.Host.BkHostInneripV6
	}

	// 其他字段处理
	bkState, srvStatus := "", ""
	if info.Host.SrvStatus != nil {
		srvStatus = *info.Host.SrvStatus
	}
	if srvStatus != "" {
		bkState = *info.Host.SrvStatus
	} else if info.Host.BkState != nil {
		bkState = *info.Host.BkState
	}
	bkProvinceName := ""
	if info.Host.BkProvinceName != nil {
		bkProvinceName = *info.Host.BkProvinceName
	}
	bkStateName := ""
	if info.Host.BkStateName != nil {
		bkStateName = *info.Host.BkStateName
	}
	bkIspName := ""
	if info.Host.BkIspName != nil {
		bkIspName = *info.Host.BkIspName
	}

	host := &AlarmHostInfo{
		BkBizId:             info.Host.BkBizId,
		BkAgentId:           info.Host.BkAgentId,
		Operator:            operator,
		BkBakOperator:       bkBakOperator,
		BkCloudId:           info.Host.BkCloudId,
		BkComment:           info.Host.BkComment,
		BkHostId:            info.Host.BkHostId,
		BkHostInnerip:       info.Host.BkHostInnerip,
		BkHostInneripV6:     info.Host.BkHostInneripV6,
		BkHostName:          info.Host.BkHostName,
		BkHostOuterip:       info.Host.BkHostOuterip,
		BkHostOuteripV6:     info.Host.BkHostOuteripV6,
		BkOsName:            info.Host.BkOsName,
		BkOsType:            info.Host.BkOsType,
		BkOsVersion:         info.Host.BkOsVersion,
		BkOsBit:             info.Host.BkOsBit,
		BkProvinceName:      bkProvinceName,
		BkIspName:           bkIspName,
		BkState:             bkState,
		BkStateName:         bkStateName,
		SrvStatus:           srvStatus,
		BkSupplierAccount:   info.Host.BkSupplierAccount,
		BkMem:               info.Host.BkMem,
		BkDisk:              info.Host.BkDisk,
		BkCpu:               info.Host.BkCpu,
		BkCpuModule:         info.Host.BkCpuModule,
		IdcUnitName:         info.Host.IdcUnitName,
		NetDeviceId:         info.Host.NetDeviceId,
		RackId:              info.Host.RackId,
		BkSvrDeviceClsName:  info.Host.BkSvrDeviceClsName,
		SvrDeviceClass:      info.Host.SvrDeviceClass,
		DockerClientVersion: info.Host.DockerClientVersion,
		DockerServerVersion: info.Host.DockerServerVersion,

		IP:          info.Host.BkHostInnerip,
		BkSetIds:    bkSetIds,
		BkModuleIds: bkModuleIds,
		BkCloudName: "",
		DisplayName: displayName,
		TopoLinks:   [][]string{},
	}

	return host
}

// HostAndTopoCacheManager 主机及拓扑缓存管理器
type HostAndTopoCacheManager struct {
	*BaseCacheManager

	hosts []*AlarmHostInfo
	topo  *cmdb.SearchBizInstTopoData

	hostIpMapping map[string][]string
}

// NewHostAndTopoCacheManager 创建主机及拓扑缓存管理器
func NewHostAndTopoCacheManager(prefix string, opt *redis.RedisOptions) (*HostAndTopoCacheManager, error) {
	manager, err := NewBaseCacheManager(prefix, opt)
	if err != nil {
		return nil, errors.Wrap(err, "new cache manager failed")
	}
	return &HostAndTopoCacheManager{
		BaseCacheManager: manager,
		hostIpMapping:    make(map[string][]string),
	}, nil
}

// BizEnabled 业务是否启用
func (m *HostAndTopoCacheManager) BizEnabled() bool {
	return true
}

// RefreshByBiz 按业务刷新缓存
func (m *HostAndTopoCacheManager) RefreshByBiz(ctx context.Context, bkBizId int) error {
	// 获取业务下的主机及拓扑信息
	hosts, topo, err := getHostAndTopoByBiz(bkBizId)
	if err != nil {
		return errors.Wrap(err, "get host by biz failed")
	}
	m.hosts = hosts
	m.topo = topo

	// 记录主机IP映射
	for _, host := range hosts {
		if host.BkHostInnerip != "" {
			m.hostIpMapping[host.BkHostInnerip] = append(m.hostIpMapping[host.BkHostInnerip], strconv.Itoa(host.BkHostId))
		}
	}

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(4)

	// 刷新topo缓存
	go func() {
		err := m.refreshTopoCache(ctx)
		if err != nil {
			logger.Error("refresh cmdb topo cache failed, err: %v", err)
		}
		waitGroup.Done()
	}()

	// 刷新主机ID缓存
	go func() {
		err := m.refreshHostIDCache(ctx)
		logger.Info("refresh cmdb host id cache")
		if err != nil {
			logger.Error("refresh cmdb host id cache failed, err: %v", err)
		}
		waitGroup.Done()
	}()

	// 刷新主机信息缓存
	go func() {
		err := m.refreshHostCache(ctx)
		logger.Info("refresh cmdb host cache")
		if err != nil {
			logger.Error("refresh cmdb host cache failed, err: %v", err)
		}
		waitGroup.Done()
	}()

	// 刷新主机AgentID缓存
	go func() {
		err := m.refreshHostAgentIDCache(ctx)
		logger.Info("refresh cmdb host agent id cache")
		if err != nil {
			logger.Error("refresh cmdb host agent id cache failed, err: %v", err)
		}
		waitGroup.Done()
	}()

	waitGroup.Wait()

	return nil
}

// RefreshGlobal 刷新全局缓存
func (m *HostAndTopoCacheManager) RefreshGlobal(ctx context.Context) error {
	// 刷新主机IP映射缓存
	key := m.GetCacheKey("cmdb.host_ip")
	data := make(map[string]string)
	for ip, hostIds := range m.hostIpMapping {
		data[ip] = fmt.Sprintf("[%s]", strings.Join(hostIds, ","))
	}
	err := m.UpdateHashMapCache(ctx, key, data)
	if err != nil {
		return errors.Wrap(err, "update hashmap cache failed")
	}
	return nil
}

// CleanGlobal 清理全局缓存
func (m *HostAndTopoCacheManager) CleanGlobal(ctx context.Context) error {
	keys := []string{
		m.GetCacheKey("cmdb.host_id"),
		m.GetCacheKey("cmdb.host_ip"),
		m.GetCacheKey("cmdb.host"),
		m.GetCacheKey("cmdb.topo"),
		m.GetCacheKey("cmdb.agent_id"),
	}

	for _, key := range keys {
		err := m.DeleteMissingHashMapFields(ctx, key)
		if err != nil {
			return errors.Wrap(err, "delete cache failed")
		}
	}
	return nil
}

// 刷新拓扑缓存
func (m *HostAndTopoCacheManager) refreshTopoCache(ctx context.Context) error {
	key := m.GetCacheKey("cmdb.topo")

	topoNodes := make(map[string]string)
	m.topo.Traverse(func(node *cmdb.SearchBizInstTopoData) {
		value, _ := json.Marshal(map[string]interface{}{
			"bk_inst_id":   node.BkInstId,
			"bk_inst_name": node.BkInstName,
			"bk_obj_id":    node.BkObjId,
			"bk_obj_name":  node.BkObjName,
		})
		topoNodes[node.GetId()] = string(value)
	})

	err := m.UpdateHashMapCache(ctx, key, topoNodes)
	if err != nil {
		return errors.Wrap(err, "update hashmap cache failed")
	}
	return nil
}

// 刷新主机ID缓存
func (m *HostAndTopoCacheManager) refreshHostIDCache(ctx context.Context) error {
	key := m.GetCacheKey("cmdb.host_id")

	hostIDs := make(map[string]string)
	for _, host := range m.hosts {
		var value string
		if host.BkHostInnerip != "" {
			value = fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId)
		} else if host.BkHostInneripV6 != "" {
			value = fmt.Sprintf("%s|%d", host.BkHostInneripV6, host.BkCloudId)
		} else {
			continue
		}
		hostIDs[strconv.Itoa(host.BkHostId)] = value
	}

	err := m.UpdateHashMapCache(ctx, key, hostIDs)
	if err != nil {
		return errors.Wrap(err, "update hashmap cache failed")
	}
	return nil
}

// 刷新主机信息缓存
func (m *HostAndTopoCacheManager) refreshHostCache(ctx context.Context) error {
	key := m.GetCacheKey("cmdb.host")
	hosts := make(map[string]string)
	for _, host := range m.hosts {
		value, _ := json.Marshal(host)
		if host.BkHostInnerip != "" {
			hosts[fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId)] = string(value)
		}
	}

	err := m.UpdateHashMapCache(ctx, key, hosts)
	if err != nil {
		return errors.Wrap(err, "update hashmap cache failed")
	}
	return nil
}

// 刷新主机AgentID缓存
func (m *HostAndTopoCacheManager) refreshHostAgentIDCache(ctx context.Context) error {
	key := m.GetCacheKey("cmdb.agent_id")

	agentIDs := make(map[string]string)
	for _, host := range m.hosts {
		if host.BkAgentId != "" {
			agentIDs[host.BkAgentId] = strconv.Itoa(host.BkHostId)
		}
	}

	err := m.UpdateHashMapCache(ctx, key, agentIDs)
	if err != nil {
		return errors.Wrap(err, "update hashmap cache failed")
	}
	return nil
}

// getHostAndTopoByBiz 查询业务下的主机及拓扑信息
func getHostAndTopoByBiz(bkBizID int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, nil, errors.Wrap(err, "get cmdb api client failed")
	}

	// 设置超时时间
	_ = cmdbApi.AddOperationOptions()

	// 批量拉取业务下的主机信息
	req := cmdbApi.ListBizHostsTopo()
	results, err := api.BatchApiRequest(
		req,
		CmdbApiPageSize,
		func(resp interface{}) (int, error) {
			var res cmdb.ListBizHostsTopoResp
			err := mapstructure.Decode(resp, &res)
			if err != nil {
				return 0, errors.Wrap(err, "decode response failed")
			}
			return res.Data.Count, nil
		},
		func(req define.Operation, page int) define.Operation {
			return req.SetBody(map[string]interface{}{"page": map[string]int{"start": page * CmdbApiPageSize, "limit": CmdbApiPageSize}, "bk_biz_id": bkBizID, "fields": HostFields})
		},
		10,
	)
	if err != nil {
		return nil, nil, err
	}
	hosts := make([]*AlarmHostInfo, 0)
	for _, result := range results {
		var res cmdb.ListBizHostsTopoResp
		err := mapstructure.Decode(result, &res)
		if err != nil {
			return nil, nil, errors.Wrap(err, "decode response failed")
		}
		for _, rawHost := range res.Data.Info {
			host := NewAlarmHostInfoByListBizHostsTopoDataInfo(&rawHost)
			host.BkBizId = bkBizID
			hosts = append(hosts, host)
		}
	}

	// 拉取云区域信息
	var cloudAreaResp cmdb.SearchCloudAreaResp
	_, err = cmdbApi.SearchCloudArea().SetBody(map[string]interface{}{"page": map[string]int{"start": 0, "limit": 1000}}).SetResult(&cloudAreaResp).Request()
	err = api.HandleApiResultError(cloudAreaResp.ApiCommonRespMeta, err, "search cloud area failed")
	if err != nil {
		return nil, nil, err
	}
	cloudIdToName := make(map[int]string)
	for _, cloudArea := range cloudAreaResp.Data.Info {
		cloudIdToName[cloudArea.BkCloudId] = cloudArea.BkCloudName
	}

	// 补充云区域名称到主机
	for _, host := range hosts {
		cloudName, ok := cloudIdToName[host.BkCloudId]
		if !ok {
			cloudName = strconv.Itoa(host.BkCloudId)
		}
		host.BkCloudName = cloudName
	}

	// 查询业务下的拓扑信息
	var bizInstTopoResp cmdb.SearchBizInstTopoResp
	_, err = cmdbApi.SearchBizInstTopo().SetBody(map[string]interface{}{"bk_biz_id": bkBizID}).SetResult(&bizInstTopoResp).Request()
	err = api.HandleApiResultError(bizInstTopoResp.ApiCommonRespMeta, err, "search biz inst topo failed")
	if err != nil {
		return nil, nil, err
	}

	// 查询业务下的内置节点
	var bizInternalModuleResp cmdb.GetBizInternalModuleResp
	_, err = cmdbApi.GetBizInternalModule().SetBody(map[string]interface{}{"bk_biz_id": bkBizID}).SetResult(&bizInternalModuleResp).Request()
	err = api.HandleApiResultError(bizInternalModuleResp.ApiCommonRespMeta, err, "get biz internal module failed")
	if err != nil {
		return nil, nil, err
	}

	// 将内置节点补充到拓扑树中
	setNode := &cmdb.SearchBizInstTopoData{
		BkInstId:   bizInternalModuleResp.Data.BkSetId,
		BkInstName: bizInternalModuleResp.Data.BkSetName,
		BkObjId:    "set",
		BkObjName:  "Set",
		Child:      []cmdb.SearchBizInstTopoData{},
	}
	for _, module := range bizInternalModuleResp.Data.Module {
		setNode.Child = append(setNode.Child, cmdb.SearchBizInstTopoData{
			BkInstId:   module.BkModuleId,
			BkInstName: module.BkModuleName,
			BkObjId:    "module",
			BkObjName:  "Module",
			Child:      []cmdb.SearchBizInstTopoData{},
		})
	}
	bizInstTopoResp.Data[0].Child = append(bizInstTopoResp.Data[0].Child, *setNode)

	// 构建模块ID到拓扑链路的映射
	moduleIdToTopoLinks := make(map[int][]string)
	bizInstTopoResp.Data[0].ToTopoLinks(&moduleIdToTopoLinks, []string{})

	// 补充拓扑信息到主机
	for _, host := range hosts {
		for _, bkModuleId := range host.BkModuleIds {
			topoLinks, ok := moduleIdToTopoLinks[bkModuleId]
			if !ok {
				continue
			}
			host.TopoLinks = append(host.TopoLinks, topoLinks)
		}
	}

	return hosts, &bizInstTopoResp.Data[0], nil
}

// CleanByEvents 通过变更事件清理缓存
func (m *HostAndTopoCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	client := m.RedisClient
	switch resourceType {
	case "host":
		agentIds := make([]string, 0)
		hostIds := make([]string, 0)
		hostKeys := make([]string, 0)

		// 提取需要删除的缓存key
		for _, event := range events {
			agentId, ok := event["bk_agent_id"].(string)
			if ok && agentId != "" {
				agentIds = append(agentIds, agentId)
			}

			hostId, ok := event["bk_host_id"].(int)
			if ok && hostId != 0 {
				hostIds = append(hostIds, strconv.Itoa(hostId))
			}

			bkHostInnerip, ok := event["bk_host_innerip"].(string)
			bkCloudId, ok := event["bk_cloud_id"].(int)
			if ok && bkHostInnerip != "" {
				hostKeys = append(hostKeys, fmt.Sprintf("%s|%d", bkHostInnerip, bkCloudId))
			}
		}

		// 删除缓存
		if len(agentIds) > 0 {
			err := client.HDel(ctx, m.GetCacheKey("cmdb.agent_id"), agentIds...).Err()
			if err != nil {
				logger.Errorf("hdel failed, key: %s, err: %v", m.GetCacheKey("cmdb.agent_id"), err)
			}
		}
		if len(hostIds) > 0 {
			err := client.HDel(ctx, m.GetCacheKey("cmdb.host_id"), hostIds...).Err()
			if err != nil {
				logger.Errorf("hdel failed, key: %s, err: %v", m.GetCacheKey("cmdb.host_id"), err)
			}
		}
		if len(hostKeys) > 0 {
			err := client.HDel(ctx, m.GetCacheKey("cmdb.host"), hostKeys...).Err()
			if err != nil {
				logger.Errorf("hdel failed, key: %s, err: %v", m.GetCacheKey("cmdb.host"), err)
			}
		}
	case "topo":
		key := m.GetCacheKey("cmdb.topo")
		topoIds := make([]string, 0)
		for _, event := range events {
			bkObjId := event["bk_obj_id"].(string)
			bkInstId := event["bk_inst_id"].(string)
			topoIds = append(topoIds, fmt.Sprintf("%s:%s", bkObjId, bkInstId))
		}
		err := client.HDel(ctx, key, topoIds...).Err()
		if err != nil {
			return errors.Wrap(err, "hdel failed")
		}
	}
	return nil
}

// UpdateByEvents 通过变更事件更新缓存
func (m *HostAndTopoCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	switch resourceType {
	case "host":
		key := m.GetCacheKey("cmdb.host")

		hostKeys := make([]string, 0)
		for _, event := range events {
			ip, ok := event["bk_host_innerip"].(string)
			bkCloudId, ok := event["bk_cloud_id"].(int)

			if ok && ip != "" {
				hostKeys = append(hostKeys, fmt.Sprintf("%s|%d", ip, bkCloudId))
			}
		}

		result := m.RedisClient.HMGet(ctx, key, hostKeys...)
		if result.Err() != nil {
			return errors.Wrap(result.Err(), "hmget failed")
		}

		var host *AlarmHostInfo
		needUpdateBizIds := make(map[int]struct{})
		for _, value := range result.Val() {
			err := json.Unmarshal([]byte(value.(string)), &host)
			if err != nil {
				continue
			}

			needUpdateBizIds[host.BkBizId] = struct{}{}
		}

		for bizID := range needUpdateBizIds {
			// todo: 业务更新
			fmt.Printf("update host cache by bizID: %d\n", bizID)
		}
	case "topo":
		key := m.GetCacheKey("cmdb.topo")
		topoNodes := make(map[string]string)
		for _, event := range events {
			bkObjId := event["bk_obj_id"].(string)
			bkInstId := event["bk_inst_id"].(string)
			topo := map[string]interface{}{
				"bk_inst_id":   event["bk_inst_id"],
				"bk_inst_name": event["bk_inst_name"],
				"bk_obj_id":    event["bk_obj_id"],
				"bk_obj_name":  event["bk_obj_name"],
			}
			value, _ := json.Marshal(topo)
			topoNodes[fmt.Sprintf("%s:%s", bkObjId, bkInstId)] = string(value)
		}
		err := m.UpdateHashMapCache(ctx, key, topoNodes)
		if err != nil {
			return errors.Wrap(err, "update hashmap cache failed")
		}
	}
	return nil
}
