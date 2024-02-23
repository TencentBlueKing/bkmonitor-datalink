// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"

	client "github.com/influxdata/influxdb1-client/v2"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// InfluxdbHostInfoSvc influxdb host info service
type InfluxdbHostInfoSvc struct {
	*storage.InfluxdbHostInfo
}

func NewInfluxdbHostInfoSvc(obj *storage.InfluxdbHostInfo) InfluxdbHostInfoSvc {
	return InfluxdbHostInfoSvc{
		InfluxdbHostInfo: obj,
	}
}

// RefreshDefaultRp 更新RP的默认配置
func (s *InfluxdbHostInfoSvc) RefreshDefaultRp() error {
	if s.InfluxdbHostInfo == nil {
		return errors.New("InfluxdbHostInfo obj can not be nil")
	}
	db := mysql.GetDBSession().DB
	// 查询机器归属哪个influxdb集群
	var clusters []storage.InfluxdbClusterInfo
	if err := storage.NewInfluxdbClusterInfoQuerySet(db).Select(storage.InfluxdbClusterInfoDBSchema.ClusterName).HostNameEq(s.HostName).All(&clusters); err != nil {
		return errors.Wrapf(err, "query for host_name [%s]'s cluster info failed", s.HostName)
	}
	if len(clusters) == 0 {
		logger.Infof("host [%s] does not belong to any cluster, skip", s.HostName)
		return nil
	}
	var clusterNames []string
	for _, c := range clusters {
		clusterNames = append(clusterNames, c.ClusterName)
	}
	// 查询该集群下有哪些结果表是需要刷新配置的
	var refreshStorageList []storage.InfluxdbStorage
	if err := storage.NewInfluxdbStorageQuerySet(db).ProxyClusterNameIn(clusterNames...).UseDefaultRpEq(true).EnableRefreshRpEq(true).All(&refreshStorageList); err != nil {
		return errors.Wrapf(err, "query for InfluxdbStorage cluster_names [%v] failed", clusterNames)
	}
	if len(refreshStorageList) == 0 {
		logger.Infof("host [%s] has no db need to refresh, skip", s.HostName)
		return nil
	}
	var refreshDBList []string
	for _, db := range refreshStorageList {
		refreshDBList = append(refreshDBList, db.Database)
	}
	refreshDBList = slicex.RemoveDuplicate(&refreshDBList)
	return s.updateDefaultRp(refreshDBList)

}

// 更新本机各个DB的RP的默认配置是否和DB的一致
func (s *InfluxdbHostInfoSvc) updateDefaultRp(databases []string) error {
	c, err := s.GetClient()
	if err != nil {
		return errors.Wrapf(err, "get influxdb [%s] client failed", s.HostName)
	}
	defer c.Close()
	// 获取默认duration配置
	defaultDurationStr := fmt.Sprintf("%vd", cfg.GlobalTsDataSavedDays)
	defaultDuration, err := timex.ParseDuration(defaultDurationStr)
	if err != nil {
		return errors.Errorf("default duration [%s] format error", defaultDurationStr)
	}
	// 获取influxdb中所有db
	resps, err := influxdb.QueryDB(c, "SHOW DATABASES", "", nil)
	if err != nil {
		return errors.Wrapf(err, "influxdb [%s] show databases faield", s.HostName)
	}
	if len(resps) == 0 {
		return errors.Errorf("influxdb [%s] show databases result is empty", s.HostName)
	}
	dbInfoList := influxdb.ParseResult(resps[0])
	for _, dbInfo := range dbInfoList {
		dbName, ok := dbInfo["name"].(string)
		if !ok {
			logger.Warnf("influxdb [%s] parse dbname failed, [%v]", s.HostName, dbInfo)
			continue
		}
		// 判断该db是否在需要刷新的数组当中，如果不是，则直接跳过
		if !slicex.IsExistItem(databases, dbName) {
			continue
		}
		// 获取该db下的retention policies
		resps, err := influxdb.QueryDB(c, "SHOW RETENTION POLICIES", dbName, nil)
		if err != nil {
			logger.Errorf("influxdb [%s] cmd [SHOW RETENTION POLICIES ON %s] failed, %v", s.HostName, dbName, err)
			continue
		}
		if len(resps) == 0 {
			logger.Warnf("influxdb [%s] [SHOW RETENTION POLICIES ON %s]  result is empty, skip", s.HostName, dbName)
			continue
		}
		rpList := influxdb.ParseResult(resps[0])
		for _, rpInfo := range rpList {
			rpInfoParse := optionx.NewOptions(rpInfo)
			// 忽略不是默认的rp配置
			isDefault, ok := rpInfoParse.GetBool("default")
			if !ok {
				logger.Errorf("influxdb [%s] db [%s] parse rp info failed, skip, %v", s.HostName, dbName, err)
				continue
			}
			if !isDefault {
				logger.Infof("influxdb [%s] db [%s] rp [%v] is not default, skip", s.HostName, dbName, rpInfo)
				continue
			}
			durationStr, ok := rpInfoParse.GetString("duration")
			if !ok {
				logger.Errorf("influxdb [%s] db [%s] parse rp duration failed, skip, %v", s.HostName, dbName, err)
				break
			}
			duration, err := timex.ParseDuration(durationStr)
			if !ok {
				logger.Errorf("influxdb [%s] db [%s] parse rp duration to go duration failed, skip, %v", s.HostName, dbName, err)
				break
			}
			// 如果发现默认配置和settings中的配置是一致的，可以直接跳过到下一个DB
			if duration == defaultDuration {
				break
			}
			// 否则需要更新配置
			// 判断出合理的shard再对RP进行修改
			shardDuration, err := storage.JudgeShardByDuration("inf")
			if err != nil {
				logger.Errorf("influxdb [%s] update default rp, JudgeShardByDuration failed, %v", s.HostName, err)
				break
			}
			name, ok := rpInfoParse.GetString("name")
			if !ok {
				logger.Errorf("influxdb [%s] db [%s] parse rp name failed, skip, %v", s.HostName, dbName, err)
				break
			}
			cmd := fmt.Sprintf("ALTER RETENTION POLICY %s ON %s DURATION %s SHARD DURATION %s DEFAULT", name, dbName, defaultDuration, shardDuration)
			if _, err := influxdb.QueryDB(c, cmd, dbName, nil); err != nil {
				logger.Errorf("influxdb [%s] db [%s] queyr [%s] failed, %v", s.HostName, dbName, cmd, err)
				break
			}
			// 默认的修改完
			break
		}
	}

	return nil

}

// GetClient 获取influxdb client
func (s InfluxdbHostInfoSvc) GetClient() (client.Client, error) {
	pwd := s.Password
	if pwd != "" {
		pwd = cipher.DBAESCipher.AESDecrypt(pwd)
	}
	return influxdb.GetClient(fmt.Sprintf("%s://%s:%v", s.Protocol, s.DomainName, s.Port), s.Username, pwd, 5)
}
