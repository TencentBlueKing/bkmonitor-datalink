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

package cmdbcache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/cast"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// hostFields 主机字段
var hostFields = []string{
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

	TopoLinks map[string][]map[string]interface{} `json:"topo_link"`

	Expands map[string]map[string]any `json:"expands"`
}

const (
	hostCacheKey        = "cmdb.host"
	hostAgentIDCacheKey = "cmdb.agent_id"
	hostIPCacheKey      = "cmdb.host_ip"
	topoCacheKey        = "cmdb.topo"
)

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

	// 补充扩展信息到 host 节点
	var expands map[string]map[string]any
	if info.Host.VersionMeta != "" {
		if err := json.Unmarshal([]byte(info.Host.VersionMeta), &expands); err != nil {
			logger.Warnf("[cmdb_relation] unmarshal error: %v, version_meta: %s", err, info.Host.VersionMeta)
		}
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
		TopoLinks:   make(map[string][]map[string]interface{}),
		Expands:     expands,
	}

	return host
}

// HostAndTopoCacheManager 主机及拓扑缓存管理器
type HostAndTopoCacheManager struct {
	*BaseCacheManager

	hostIpMap     map[string]map[string]struct{}
	hostIpMapLock sync.RWMutex
}

// NewHostAndTopoCacheManager 创建主机及拓扑缓存管理器
func NewHostAndTopoCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*HostAndTopoCacheManager, error) {
	manager, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, errors.Wrap(err, "new cache Manager failed")
	}

	manager.initUpdatedFieldSet(hostCacheKey, hostAgentIDCacheKey, hostIPCacheKey, topoCacheKey)
	return &HostAndTopoCacheManager{
		BaseCacheManager: manager,
		hostIpMap:        make(map[string]map[string]struct{}),
	}, nil
}

// Type 缓存类型
func (m *HostAndTopoCacheManager) Type() string {
	return "host_topo"
}

// RefreshByBiz 按业务刷新缓存
func (m *HostAndTopoCacheManager) RefreshByBiz(ctx context.Context, bkBizId int) error {
	// 业务是资源池时，不需要刷新
	if cfg.EnableMultiTenantMode {
		if bkBizId == 2 {
			return nil
		}
	} else {
		if bkBizId == 1 {
			return nil
		}
	}

	logger.Infof("start refresh cmdb cache by biz: %d", bkBizId)
	startTime := time.Now()
	defer func() {
		logger.Infof("end refresh cmdb cache by biz: %d, cost: %f", bkBizId, time.Since(startTime).Seconds())
	}()

	// 获取业务下的主机及拓扑信息
	hosts, topo, err := getHostAndTopoByBiz(ctx, m.GetBkTenantId(), bkBizId)
	if err != nil {
		return errors.Wrap(err, "get host by biz failed")
	}

	// 记录主机IP映射
	m.hostIpMapLock.Lock()
	for _, host := range hosts {
		if host.BkHostInnerip != "" {
			if _, ok := m.hostIpMap[host.BkHostInnerip]; !ok {
				m.hostIpMap[host.BkHostInnerip] = make(map[string]struct{})
			}
			m.hostIpMap[host.BkHostInnerip][fmt.Sprintf("\"%s|%d\"", host.BkHostInnerip, host.BkCloudId)] = struct{}{}
		}
	}
	m.hostIpMapLock.Unlock()

	wg := sync.WaitGroup{}
	wg.Add(4)

	// 刷新topo缓存
	go func() {
		err := m.refreshTopoCache(ctx, bkBizId, topo)
		if err != nil {
			logger.Error("refresh cmdb topo cache failed, err: %v", err)
		}
		wg.Done()
	}()

	// 刷新主机信息缓存
	go func() {
		err := m.refreshHostCache(ctx, bkBizId, hosts)
		if err != nil {
			logger.Error("refresh cmdb host cache failed, err: %v", err)
		}
		wg.Done()
	}()

	// 刷新主机AgentID缓存
	go func() {
		err := m.refreshHostAgentIDCache(ctx, bkBizId, hosts)
		if err != nil {
			logger.Error("refresh cmdb host agent id cache failed, err: %v", err)
		}
		wg.Done()
	}()

	// 刷新 relation metrics 缓存
	go func() {
		infos := m.HostToRelationInfos(hosts)
		err = relation.GetRelationMetricsBuilder().BuildInfosCache(ctx, bkBizId, relation.Host, infos)
		if err != nil {
			logger.Error("refresh relation metrics failed, err: %v", err)
		}
		wg.Done()
	}()

	wg.Wait()
	return nil
}

// HostToRelationInfos 主机信息转关联缓存信息
func (m *HostAndTopoCacheManager) HostToRelationInfos(hosts []*AlarmHostInfo) []*relation.Info {
	infos := make([]*relation.Info, 0, len(hosts))
	for _, h := range hosts {
		if h.BkHostId == 0 {
			continue
		}

		hostID := cast.ToString(h.BkHostId)
		hostItem := relation.Item{
			ID:       hostID,
			Resource: relation.Host,
			Label: map[string]string{
				relation.HostID: hostID,
			},
		}

		// 忽略 ip 过长的异常数据，使用逗号存放多 ip 的场景
		if h.BkHostInnerip != "" && !strings.Contains(h.BkHostInnerip, ",") && len(h.BkHostInnerip) < 50 {
			systemInfo := &relation.Info{}
			systemID := fmt.Sprintf("%s|%d", h.BkHostInnerip, h.BkCloudId)
			systemInfo.ID = systemID
			systemInfo.Resource = relation.System
			systemInfo.Label = map[string]string{
				"bk_target_ip": h.BkHostInnerip,
				"bk_cloud_id":  fmt.Sprintf("%d", h.BkCloudId),
			}
			systemInfo.Links = []relation.Link{{hostItem}}
			infos = append(infos, systemInfo)
		}

		hostInfo := &relation.Info{
			ID:       hostID,
			Resource: relation.Host,
			Label:    hostItem.Label,
			Expands:  relation.TransformExpands(h.Expands),
		}
		if hostInfo.Expands[relation.Host] != nil {
			hostInfo.Expands[relation.Host][relation.HostName] = h.BkHostName
		}

		for _, tplink := range h.TopoLinks {
			var link []relation.Item
			for _, tp := range tplink {
				resource := cast.ToString(tp["bk_obj_id"])
				if resource == "" {
					continue
				}

				id := cast.ToString(tp["bk_inst_id"])
				if id == "" {
					continue
				}

				link = append(link, relation.Item{
					ID:       id,
					Resource: resource,
					Label: map[string]string{
						fmt.Sprintf("bk_%s_id", resource): id,
					},
				})
			}

			if len(link) > 0 {
				hostInfo.Links = append(hostInfo.Links, link)
			}
		}

		// 写入 host 数据
		infos = append(infos, hostInfo)
	}

	return infos
}

// RefreshGlobal 刷新全局缓存
func (m *HostAndTopoCacheManager) RefreshGlobal(ctx context.Context) error {
	// 刷新主机IP映射缓存
	key := m.GetCacheKey(hostIPCacheKey)
	data := make(map[string]string)
	for ip, hostIdMapping := range m.hostIpMap {
		hostIds := make([]string, 0, len(hostIdMapping))
		for hostId := range hostIdMapping {
			hostIds = append(hostIds, hostId)
		}
		data[ip] = fmt.Sprintf("[%s]", strings.Join(hostIds, ","))
	}

	err := m.UpdateHashMapCache(ctx, key, data)
	if err != nil {
		return errors.Wrap(err, "update host ip cache failed")
	}

	// 刷新缓存过期时间
	for _, key := range []string{hostCacheKey, topoCacheKey, hostAgentIDCacheKey, hostIPCacheKey} {
		if err := m.RedisClient.Expire(ctx, m.GetCacheKey(key), m.Expire).Err(); err != nil {
			logger.Error("set cache expire time failed, key: %s, err: %v", key, err)
		}
	}

	return nil
}

// Reset 重置
func (m *HostAndTopoCacheManager) Reset() {
	m.BaseCacheManager.Reset()
	m.hostIpMap = make(map[string]map[string]struct{})
}

// CleanGlobal 清理全局缓存
func (m *HostAndTopoCacheManager) CleanGlobal(ctx context.Context) error {
	keys := []string{
		m.GetCacheKey(hostIPCacheKey),
		m.GetCacheKey(hostCacheKey),
		m.GetCacheKey(topoCacheKey),
		m.GetCacheKey(hostAgentIDCacheKey),
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
func (m *HostAndTopoCacheManager) refreshTopoCache(ctx context.Context, bkBizId int, topo *cmdb.SearchBizInstTopoData) error {
	key := m.GetCacheKey(topoCacheKey)

	topoNodes := make(map[string]string)
	topo.Traverse(func(node *cmdb.SearchBizInstTopoData) {
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
		return errors.Wrap(err, "update cmdb topo hashmap cache failed")
	}

	logger.Infof("refresh cmdb topo by biz: %d, topo count: %d", bkBizId, len(topoNodes))
	return nil
}

// 刷新主机信息缓存
func (m *HostAndTopoCacheManager) refreshHostCache(ctx context.Context, bkBizId int, hosts []*AlarmHostInfo) error {
	key := m.GetCacheKey(hostCacheKey)
	hostMapping := make(map[string]string)
	for _, host := range hosts {
		value, _ := json.Marshal(host)
		if host.BkHostInnerip != "" {
			hostMapping[fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId)] = string(value)
		}
		if host.BkHostId > 0 {
			hostMapping[strconv.Itoa(host.BkHostId)] = string(value)
		}
	}

	err := m.UpdateHashMapCache(ctx, key, hostMapping)
	if err != nil {
		return errors.Wrap(err, "update cmdb host hashmap cache failed")
	}

	logger.Infof("refresh cmdb host by biz: %d, host count: %d", bkBizId, len(hostMapping))
	return nil
}

// 刷新主机AgentID缓存
func (m *HostAndTopoCacheManager) refreshHostAgentIDCache(ctx context.Context, bkBizId int, hosts []*AlarmHostInfo) error {
	key := m.GetCacheKey(hostAgentIDCacheKey)

	agentIDs := make(map[string]string)
	for _, host := range hosts {
		if host.BkAgentId != "" {
			agentIDs[host.BkAgentId] = strconv.Itoa(host.BkHostId)
		}
	}

	err := m.UpdateHashMapCache(ctx, key, agentIDs)
	if err != nil {
		return errors.Wrap(err, "update hashmap cmdb host agent id cache failed")
	}

	logger.Infof("refresh cmdb host agent id by biz: %d, agent id count: %d", bkBizId, len(agentIDs))
	return nil
}

// getHostAndTopoByBiz 查询业务下的主机及拓扑信息
func getHostAndTopoByBiz(ctx context.Context, bkTenantId string, bkBizID int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
	cmdbApi := getCmdbApi(bkTenantId)

	// 设置超时时间
	_ = cmdbApi.AddOperationOptions()

	// 批量拉取业务下的主机信息
	results, err := api.BatchApiRequest(
		cmdbApiPageSize,
		func(resp interface{}) (int, error) {
			var res cmdb.ListBizHostsTopoResp
			err := mapstructure.Decode(resp, &res)
			if err != nil {
				return 0, errors.Wrap(err, "decode response failed")
			}
			return res.Data.Count, nil
		},
		func(page int) define.Operation {
			return cmdbApi.ListBizHostsTopo().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bkBizID)}).SetBody(map[string]interface{}{"page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}, "bk_biz_id": bkBizID, "fields": hostFields})
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
	_, err = cmdbApi.SearchCloudArea().SetContext(ctx).SetBody(map[string]interface{}{"page": map[string]int{"start": 0, "limit": 1000}}).SetResult(&cloudAreaResp).Request()
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
	_, err = cmdbApi.SearchBizInstTopo().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bkBizID)}).SetBody(map[string]interface{}{"bk_biz_id": bkBizID}).SetResult(&bizInstTopoResp).Request()
	err = api.HandleApiResultError(bizInstTopoResp.ApiCommonRespMeta, err, "search biz inst topo failed")
	if err != nil {
		logger.Errorf("search biz inst topo failed, bk_biz_id: %d, err: %v", bkBizID, err)
		return nil, nil, err
	}

	if len(bizInstTopoResp.Data) == 0 {
		return hosts, nil, nil
	}

	// 查询业务下的内置节点
	var bizInternalModuleResp cmdb.GetBizInternalModuleResp
	_, err = cmdbApi.GetBizInternalModule().SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bkBizID)}).SetBody(map[string]interface{}{"bk_biz_id": bkBizID}).SetResult(&bizInternalModuleResp).Request()
	err = api.HandleApiResultError(bizInternalModuleResp.ApiCommonRespMeta, err, "get biz internal module failed")
	if err != nil {
		logger.Errorf("get biz internal module failed, bk_biz_id: %d, err: %v", bkBizID, err)
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
	moduleIdToTopoLinks := make(map[int][]map[string]interface{})
	bizInstTopoResp.Data[0].ToTopoLinks(&moduleIdToTopoLinks, []map[string]interface{}{})

	// 补充拓扑信息到主机
	for _, host := range hosts {
		for _, bkModuleId := range host.BkModuleIds {
			if topoLink, ok := moduleIdToTopoLinks[bkModuleId]; ok {
				host.TopoLinks[fmt.Sprintf("module|%d", bkModuleId)] = topoLink
			}
		}
	}

	return hosts, &bizInstTopoResp.Data[0], nil
}

// CleanByEvents 通过变更事件清理缓存
func (m *HostAndTopoCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	if len(events) == 0 {
		return nil
	}

	client := m.RedisClient
	switch resourceType {
	case "host":
		agentIds := make([]string, 0)
		hostKeys := make([]string, 0)

		// 提取需要删除的缓存key
		for _, event := range events {
			agentId, ok := event["bk_agent_id"].(string)
			if ok && agentId != "" {
				agentIds = append(agentIds, agentId)
			}

			hostId, ok := event["bk_host_id"].(float64)
			if ok && hostId != 0 {
				hostKeys = append(hostKeys, strconv.Itoa(int(hostId)))
			}

			ip, ok := event["bk_host_innerip"].(string)
			bkCloudId, ok := event["bk_cloud_id"].(float64)
			if ok && ip != "" {
				hostKeys = append(hostKeys, fmt.Sprintf("%s|%d", ip, int(bkCloudId)))
			}
		}

		// 删除缓存
		if len(agentIds) > 0 {
			err := client.HDel(ctx, m.GetCacheKey(hostAgentIDCacheKey), agentIds...).Err()
			if err != nil {
				logger.Errorf("hdel failed, key: %s, err: %v", m.GetCacheKey(hostAgentIDCacheKey), err)
			}
		}
		if len(hostKeys) > 0 {
			// 清理 relationMetrics 里的缓存数据
			rmb := relation.GetRelationMetricsBuilder()
			result := m.RedisClient.HMGet(ctx, m.GetCacheKey(hostCacheKey), hostKeys...)
			for _, value := range result.Val() {
				// 如果找不到对应的缓存，不需要更新
				if value == nil {
					continue
				}

				var host *AlarmHostInfo
				err := json.Unmarshal([]byte(value.(string)), &host)
				if err != nil {
					continue
				}

				// 清理 relation metrics 里面的 host
				rmb.ClearResourceWithID(host.BkBizId, relation.Host, cast.ToString(host.BkHostId))
			}

			// 记录需要更新的业务ID
			err := client.HDel(ctx, m.GetCacheKey(hostCacheKey), hostKeys...).Err()
			if err != nil {
				logger.Errorf("hdel failed, key: %s, err: %v", m.GetCacheKey(hostCacheKey), err)
			}
		}
	case "mainline_instance":
		key := m.GetCacheKey(topoCacheKey)
		topoIds := make([]string, 0)
		for _, event := range events {
			bkObjId := event["bk_obj_id"].(string)
			bkInstId, ok := event["bk_inst_id"].(float64)
			if !ok {
				continue
			}
			topoIds = append(topoIds, fmt.Sprintf("%s|%d", bkObjId, int(bkInstId)))
		}
		if len(topoIds) == 0 {
			return nil
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
	if len(events) == 0 {
		return nil
	}

	needCleanAgentIds := make(map[string]struct{})
	needCleanHostKeys := make(map[string]struct{})
	needUpdateBizIds := make(map[int]struct{})
	switch resourceType {
	case "host":
		key := m.GetCacheKey(hostCacheKey)
		// 提取需要更新的缓存key
		for _, event := range events {
			cacheKeys := make([]string, 0)

			ip, ok := event["bk_host_innerip"].(string)
			bkCloudId, ok := event["bk_cloud_id"].(float64)
			hostKey := ""

			if ok && ip != "" {
				hostKey = fmt.Sprintf("%s|%d", ip, int(bkCloudId))
				cacheKeys = append(cacheKeys, hostKey)
			}

			bkHostId, ok := event["bk_host_id"].(float64)
			if ok && bkHostId > 0 {
				cacheKeys = append(cacheKeys, strconv.Itoa(int(bkHostId)))
			}

			result := m.RedisClient.HMGet(ctx, key, cacheKeys...)
			if result.Err() != nil {
				return errors.Wrapf(result.Err(), "hmget failed, key: %s", key)
			}

			agentId, ok := event["bk_agent_id"].(string)

			for _, value := range result.Val() {
				if value == nil {
					continue
				}
				var host *AlarmHostInfo
				err := json.Unmarshal([]byte(value.(string)), &host)
				if err != nil {
					continue
				}
				needUpdateBizIds[host.BkBizId] = struct{}{}

				// 如果有agentId变更，需要清理agentId缓存
				if ok && agentId != host.BkAgentId && host.BkAgentId != "" {
					needCleanAgentIds[host.BkAgentId] = struct{}{}
				}

				// 如果有ip变更，需要清理ip缓存
				if host.BkHostInnerip != "" {
					oldHostKey := fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId)
					if hostKey != oldHostKey {
						needCleanHostKeys[oldHostKey] = struct{}{}
					}
				}
			}
		}
	case "mainline_instance":
		key := m.GetCacheKey(topoCacheKey)
		topoNodes := make(map[string]string)
		for _, event := range events {
			bkObjId := event["bk_obj_id"].(string)
			bkInstId := event["bk_inst_id"].(float64)
			topo := map[string]interface{}{
				"bk_inst_id":   int(bkInstId),
				"bk_inst_name": event["bk_inst_name"],
				"bk_obj_id":    bkObjId,
				"bk_obj_name":  event["bk_obj_name"],
			}
			value, _ := json.Marshal(topo)
			topoNodes[fmt.Sprintf("%s|%d", bkObjId, int(bkInstId))] = string(value)
		}
		err := m.UpdateHashMapCache(ctx, key, topoNodes)
		if err != nil {
			return errors.Wrapf(err, "update hashmap cache failed, key: %s", key)
		}
	case "host_relation":
		for _, event := range events {
			bkBizID, ok := event["bk_biz_id"].(float64)
			if !ok {
				continue
			}
			needUpdateBizIds[int(bkBizID)] = struct{}{}
		}
	}

	// 记录需要更新的业务ID
	needUpdateBizIdSlice := make([]string, 0, len(needUpdateBizIds))
	for bizID := range needUpdateBizIds {
		needUpdateBizIdSlice = append(needUpdateBizIdSlice, strconv.Itoa(bizID))
	}
	logger.Infof("need update host cache biz ids: %v", strings.Join(needUpdateBizIdSlice, ","))

	// 按业务刷新缓存
	wg := sync.WaitGroup{}
	limitChan := make(chan struct{}, m.ConcurrentLimit)
	for bizID := range needUpdateBizIds {
		wg.Add(1)
		limitChan <- struct{}{}

		go func(bizId int) {
			defer func() {
				<-limitChan
				wg.Done()
			}()
			err := m.RefreshByBiz(ctx, bizId)
			if err != nil {
				logger.Errorf("failed to refresh host cache by biz: %d, err: %v", bizId, err)
			}
		}(bizID)
	}
	wg.Wait()

	// 清理agentId缓存
	if len(needCleanAgentIds) > 0 {
		key := m.GetCacheKey(hostAgentIDCacheKey)
		agentIds := make([]string, 0, len(needCleanAgentIds))
		for agentId := range needCleanAgentIds {
			agentIds = append(agentIds, agentId)
		}

		logger.Infof("clean agent id cache, agent ids: %v", agentIds)
		err := m.RedisClient.HDel(ctx, key, agentIds...).Err()
		if err != nil {
			logger.Errorf("hdel failed, key: %s, err: %v", key, err)
		}
	}

	// 清理ip缓存
	if len(needCleanHostKeys) > 0 {
		key := m.GetCacheKey(hostCacheKey)
		hostKeys := make([]string, 0, len(needCleanHostKeys))
		for hostKey := range needCleanHostKeys {
			hostKeys = append(hostKeys, hostKey)
		}

		logger.Infof("clean host cache, host keys: %v", hostKeys)
		err := m.RedisClient.HDel(ctx, key, hostKeys...).Err()
		if err != nil {
			logger.Errorf("hdel failed, key: %s, err: %v", key, err)
		}
	}
	return nil
}
