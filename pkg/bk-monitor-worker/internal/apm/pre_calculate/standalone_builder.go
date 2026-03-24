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
	"context"
	"os"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type PreCalculateProcessorStandLone interface {
	RunWithStandLone(filePath string)
	WatchConnections(filePath string)
}

func (p *Precalculate) RunWithStandLone(filePath string) {
	go p.WatchConnections(filePath)
	// consul is disabled in stand-lone
	for {
		select {
		case signal := <-p.readySignalChan:
			apmLogger.Infof("Pre-calculation with dataId: %s was received.", signal.startInfo.DataId)
			p.launch(signal.ctx, signal.startInfo, signal.config, signal.errorReceiveChan)
		case <-p.ctx.Done():
			apmLogger.Info("Precalculate[MAIN] received the stop signal.")
			return
		}
	}
}

type Connection struct {
	DataId           string `yaml:"dataId"`
	Token            string `yaml:"token"`
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
		logger.Errorf("Open connections file: %s failed, error: %s", filePath, err)
		return
	}

	for _, c := range lastConnectionList.Connections {
		logger.Infof("🥛 Add BkBizId: %s AppName: %s to task", c.BkBizId, c.AppName)
		p.StartByConnection(c)
	}
	logger.Infof("Started %d connections", len(lastConnectionList.Connections))

	for {
		newConnectionList, err := checkNewConnection(filePath)
		if err != nil {
			logger.Errorf("Open connections file: %s failed, error: %s", filePath, err)
		} else if len(newConnectionList.Connections) > len(lastConnectionList.Connections) {
			newConnection := newConnectionList.Connections[len(newConnectionList.Connections)-1]
			logger.Infof(
				"🌳 Detect new connection! bkBizId: %s appName: %s",
				newConnection.BkBizId, newConnection.AppName,
			)
			go p.StartByConnection(newConnection)
			lastConnectionList = newConnectionList
		}
		time.Sleep(time.Minute)
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
		conn.Token,
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
	config := p.MergeConfig(p.defaultConfig, PrecalculateOption{
		storageConfig: []storage.ProxyOption{storage.CacheBackend(storage.CacheTypeMemory)},
	})
	c := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	go p.checkError(conn.BkBizId, conn.AppName, c, cancel)
	p.readySignalChan <- readySignal{
		startInfo:        StartInfo{DataId: conn.DataId},
		config:           config,
		errorReceiveChan: c,
		ctx:              ctx,
	}
}

func (p *Precalculate) checkError(bkBizId, appName string, errorReceiveChan chan error, cancel context.CancelFunc) {
	for {
		select {
		case msg := <-errorReceiveChan:
			logger.Warnf("💥 Receive error from chan, bkBizId: %s appName: %s error: %s", bkBizId, appName, msg)
			cancel()
			return
		}
	}
}
