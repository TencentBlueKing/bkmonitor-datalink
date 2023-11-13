// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Connection 测试类 用于监听文件变化来启动一个应用的预计算
type Connection struct {
	DataId           string `yaml:"dataId"`
	BkBizId          string `yaml:"bkBizId"`
	BkBizName        string `yaml:"bkBizName"`
	AppId            string `yaml:"appId"`
	AppName          string `yaml:"appName"`
	KafkaHost        string `yaml:"kafkaHost"`
	KafkaUsername    string `yaml:"kafkaUsername"`
	KafkaPassword    string `yaml:"kafkaPassword"`
	KafkaTopic       string `yaml:"kafkaTopic"`
	TraceEsIndexName string `yaml:"traceEsIndexName"`
	TraceEsHost      string `yaml:"traceEsHost"`
	TraceEsUsername  string `yaml:"traceEsUsername"`
	TraceEsPassword  string `yaml:"traceEsPassword"`
	SaveEsIndexName  string `yaml:"saveEsIndexName"`
	SaveEsHost       string `yaml:"saveEsHost"`
	SaveEsUsername   string `yaml:"saveEsUsername"`
	SaveEsPassword   string `yaml:"saveEsPassword"`
}

type ConnectionList struct {
	Connections []Connection `yaml:"connections"`
}

func (p *Precalculate) WatchConnections(filePath string) {

	logger.Infof("Listening for connections file: %s", filePath)

	lastConnectionList, err := checkNewConnection(filePath)
	if err != nil {
		logger.Errorf("open connections file: %s failed, error: %s", filePath, err)
	}

	for _, c := range lastConnectionList.Connections {
		p.StartByConnection(c)
	}

	for {
		newConnectionList, err := checkNewConnection(filePath)
		if err != nil {
			logger.Errorf("open connections file: %s failed, error: %s", filePath, err)
		} else if len(newConnectionList.Connections) > len(lastConnectionList.Connections) {
			newConnection := newConnectionList.Connections[len(newConnectionList.Connections)-1]
			logger.Infof(
				"connections: kafkaHost: %s kafkaTopic: %s has been added to the file.",
				newConnection.KafkaHost, newConnection.KafkaTopic,
			)
			go p.StartByConnection(newConnection)
			lastConnectionList = newConnectionList
		}
		time.Sleep(5 * time.Second)
	}
}

func checkNewConnection(filePath string) (ConnectionList, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ConnectionList{}, err
	}

	var newConnectionList ConnectionList
	err = yaml.Unmarshal(data, &newConnectionList)
	if err != nil {
		return ConnectionList{}, err
	}

	return newConnectionList, nil
}

func (p *Precalculate) StartByConnection(conn Connection, _ ...PrecalculateOption) {
	center := core.GetMetadataCenter()
	center.AddDataIdAndInfo(
		conn.DataId,
		core.DataIdInfo{
			BaseInfo: core.BaseInfo{
				BkBizId:   conn.BkBizId,
				BkBizName: conn.BkBizName,
				AppId:     conn.AppId,
				AppName:   conn.AppName,
			},
			TraceEs: core.TraceEsConfig{
				IndexName: conn.TraceEsIndexName,
				Host:      conn.TraceEsHost,
				Username:  conn.TraceEsUsername,
				Password:  conn.TraceEsPassword,
			},
			SaveEs: core.TraceEsConfig{
				IndexName: conn.SaveEsIndexName,
				Host:      conn.SaveEsHost,
				Username:  conn.SaveEsUsername,
				Password:  conn.SaveEsPassword,
			},
			TraceKafka: core.TraceKafkaConfig{
				Topic:    conn.KafkaTopic,
				Host:     conn.KafkaHost,
				Username: conn.KafkaUsername,
				Password: conn.KafkaPassword,
			},
		},
	)
	p.readySignalChan <- readySignal{dataId: conn.DataId, config: p.defaultConfig}
}
