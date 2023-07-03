// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

const (
	maxPingTime = 5
)

// isKafkaServerError判断某个异常是否kafka server中的异常
func isKafkaServerError(err error) bool {
	switch err {
	// 节点异常
	case sarama.ErrBrokerNotAvailable:
		return true
	// topic异常
	case sarama.ErrUnknownTopicOrPartition:
		return true
	// 无leader，可能kafka存在脑裂的情况
	case sarama.ErrLeaderNotAvailable:
		return true
	// topic异常
	case sarama.ErrInvalidTopic:
		return true
	// 版本异常
	case sarama.ErrUnsupportedVersion:
		return true
	// kafka磁盘满
	case sarama.ErrKafkaStorageError:
		return true
	case sarama.ErrOutOfBrokers:
		return true
	// 默认返回不是server的异常问题
	default:
		return false
	}
}

func containAnyErr(str string, items []string) bool {
	for _, item := range items {
		if strings.Contains(str, item) {
			return true
		}
	}
	return false
}

// 根据报错类型，判断是否需要重新建立连接
func shouldRestablishConnection(err error) bool {
	switch err {
	// 节点异常
	case sarama.ErrBrokerNotAvailable:
		return true
	case sarama.ErrClosedClient:
		return true
	case sarama.ErrOutOfBrokers:
		return true
	case sarama.ErrNotConnected:
		return true
	}

	if containAnyErr(err.Error(), []string{"connection timed out", "connection refused", "broken pipe"}) {
		return true
	}

	// 默认返回不是server的异常问题
	return false
}

// init: create a goroutine to ping influxDB backend
// If there has 3 Times ping failed, ALL DATA WILL PUSH INTO backup storage, and this backend become unreadable.
// And after 3 Times ping is success, backend will be writable and make a goroutine to pull data from kafka to influxDB.
// Backend will be readable after all the data is pull from the backup storage.
func (b *Backend) init() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Infof("backend start init")

	// make the first ping, get the init status
	cost, _, err := b.Ping(maxPingTime * time.Second)
	if err != nil {
		flowLog.Errorf("init ping failed:%s, cost time:%s", err, cost)
		b.notifyBackendStatus(true, false, false)
		flowLog.Warnf("init notify write true and read false status")
	} else {
		// no failed data to push influx db from kafka, notify write and read true status
		if flag, err := b.backupStorage.HasData(); !flag && err == nil {
			b.notifyBackendStatus(true, true, true)
			flowLog.Debugf("init notify write and read true status")
		} else {
			b.notifyBackendStatus(true, true, false)
			flowLog.Warnf("init notify write true and read false status")
		}
	}

	b.wg.Add(1)
	// loop to update backend status
	go func() {
		t := time.NewTicker(maxPingTime * time.Second)
		for {
			select {
			case <-b.ctx.Done():
				flowLog.Warnf("recv context done, begin to clean")
				t.Stop()
				flowLog.Info("cancel func done")
				close(b.statusChannel)
				flowLog.Info("close status channel done")
				b.wg.Done()
				flowLog.Warnf("recv context done, finish now")
				return
			case <-t.C:
				flowLog.Tracef("start checkInfluxDBStatus")
				b.checkInfluxDBStatus()
			}
		}
	}()
	flowLog.Infof("backend init done")
	return nil
}

// only status change will trigger status event
// healthy -> unhealthy(three time ping failed)
// or unhealthy -> healthy
func (b *Backend) checkInfluxDBStatus() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")

	cost, _, err := b.Ping(maxPingTime * time.Second)
	// check if ping is failed
	if err != nil {
		// when ping failed, we assert that the influxDB backend is unavailable now
		b.invalidCountInc()
		flowLog.Errorf("ping failed:%s, cost time:%v, invalid count %d", err, cost, b.getInvalidCount())

		// only count equal 3 to send event, more than 3 no event again to avoid much event
		if b.getInvalidCount() >= 3 {
			b.setInnerWriteUnhealthy()
			b.setReadUnhealthy()
			b.sendStatusEvent()
			flowLog.Warnf("push inner_write->[false] read->[false] status to channel success")

			// now the backend is unavailable, we should stop the recovery
			if b.backupCancelFunc != nil {
				flowLog.Warnf("ping is failed and cancel func exists, is called to stop now.")
				b.backupCancelFunc()
			}
			return
		}
		if b.getInvalidCount() > 3 {
			flowLog.Warnf("notify is more than 3 times, no more status will be send to the channel.")
		}

		return
	}
	// In the section below it will be unhealthy -> healthy or
	// healthy backend need to check if all the kafka data is push into influxDB
	// only care about the write status, read status  will be change in kafka service.
	b.notifyBackendStatus(true, true, b.status.Read)

	// As the backend is recovery now, we should Pull data from backup storage to backend
	// But if there is any recovery doing, we should do nothing
	hasData, err := b.backupStorage.HasData()
	if err != nil {
		flowLog.Errorf("kafka check has data failed,error:%s", err)
		if isKafkaServerError(err) {
			flowLog.Errorf("kafka broker connect failed or kafka inner error,turn kafka status to down")
			KafkaStatusDown(b.name, flowLog)
			b.notifyBackendStatus(true, true, true)
		}

		if shouldRestablishConnection(err) {
			flowLog.Warnf("restablishing kafka connection...")
			b.backupStorage.Close()
			newStorage, err := newKafkaBackup(b.ctx, b.backupStorage.Topic())
			if err != nil {
				flowLog.Warnf("restablish kafka connection failed,error:%s", err)
				return
			}
			b.backupStorage = newStorage
			flowLog.Warnf("restablish kafka finished")
		}
		return
	}
	flowLog.Debugf("kafka is alive,continue to check data")
	KafkaStatusUp(b.name, flowLog)

	if hasData {
		flowLog.Infof("kafka has data,try to start recovery")
		if b.backupCancelFunc != nil {
			flowLog.Warnf("cancelFunc is not nil, there is something doing?")
		} else {
			b.backupCtx, b.backupCancelFunc = context.WithCancel(b.ctx)
			flowLog.Tracef("new cancel function->[%v]", b.backupCancelFunc)
			go func() {
				// clean up all the config for current pull round
				defer func() {
					b.backupCtx = nil
					b.backupCancelFunc = nil
					flowLog.Tracef("backupCancelFun now set to->[%v]", b.backupCancelFunc)
				}()
				start := time.Now()
				limiter := b.BackupLimiter()
				b.BackupCountInc()
				limiter.Wait(b.ctx)
				flowLog.Infof("rate limit wait pull [%s], backup_count: %d, rate_limit: %f, cost_time: %s", b.Name(), b.BackupCount(), limiter.Limit(), time.Now().Sub(start).String())
				flowLog.Debugf("start pull")
				if err := b.backupStorage.Pull(b.backupCtx, b.recoveryData); err != nil {
					flowLog.Errorf("failed to Pull data from backup storage for->[%#v]", err)
					return
				}
				flowLog.Debugf("pull data from backup storage success.")
				// all the data is recovery, notify the backend is recovery.
				if b.isInnerWriteHealthy() {
					flowLog.Warnf("PUll data done and it is inner healthy, make the backend readable.")
					b.notifyBackendStatus(true, true, true)
				}
			}()
		}
	} else {
		b.notifyBackendStatus(true, true, true)
	}

	// Once the backend is available reset all count
	b.resetInvalidCount()
	// if check backend is alive, we should update the version
	b.updateVersion()
	flowLog.Tracef("%s alive", b)
	flowLog.Tracef("done")
}

// recoveryData: callback handler for backupStorage, data will be the data save to the storage.
func (b *Backend) recoveryData(data string) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("recovery start")

	// 判断是否开启备份恢复开关，limiter 为 -1，则直接丢弃
	metricFunc := b.metric.BackupCountInc
	limiter := b.BackupLimiter()
	if limiter.Limit() < 0 {
		metricFunc("", "dropped", flowLog)
		flowLog.Warnf("backup is closed, %f", limiter.Limit())
		return
	}

	defer func() {
		if p := recover(); p != nil {
			err := common.PanicCountInc()
			if err != nil {
				flowLog.Errorf("panic count inc failed,error:%s", err)
			}
			flowLog.Errorf("get panic while recovery data,panic info:%v,panic data:%s", p, data)
			// 打印堆栈
			var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			flowLog.Errorf("panic stack ==> %s\n", buf[:n])
		}
	}()

	var err error
	// find the db and sql string
	var result Data
	reader := strings.NewReader(data)
	if err := LoadsBackendData(reader, &result); err != nil {
		flowLog.Errorf("failed to load data from backend for->[%#v]", err)
		// return
	}

	originData := result.Query
	header := result.Header
	urlParams := result.URLParams
	flowID := result.FlowID
	var resp *backend.Response

	// if there is not database name, it must be create database.
	if urlParams.DB == "" {
		flowLog.Debugf("start recover create db")
		// 建库语句的sql存储在origindata里
		dbURLParams := backend.NewQueryParams("", originData, "", "", "", "")
		// create database does need the precision args
		resp, err = b.createDatabaseWithMetric(flowID, dbURLParams, header, b.metric.RecoverCreateDBCountInc)

	} else {
		flowLog.Debugf("start recover write")
		reader := backend.NewPointsReaderWithBytes([]byte(originData))
		resp, err = b.writeWithMetric(flowID, urlParams, reader, header, b.metric.RecoverWriteCountInc)
	}

	if err != nil {
		flowLog.Errorf("fail to recovery data->[%s] from backup storage.,error:%v", data, err)
		return
	}
	// wrong status data sholud not be reBackup,it may cause same error in db instance when recovery
	if resp.Code >= 300 {
		flowLog.Errorf("fail to recovery data->[%s] from backup storage,for influxdb instance return an not 200 response,status code:%v", data, resp.Code)
		return
	}
	// if write failed, we should push the data back to the storage
	// 写入失败的备份由write和createdb自己执行，这里不需要添加逻辑

	flowLog.Tracef("success to recovery data->[%s] from backup storage.,response:%v", data, resp.Result)
	flowLog.Debugf("recovery done")
	return
}

func (b *Backend) sendStatusEvent() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		b.statusLock.RUnlock()
		flowLog.Tracef("release RLock")
	}()

	// update metric status
	b.metric.SetAlive("innerWrite", b.status.InnerWrite, flowLog)
	flowLog.Tracef("done")
}

func (b *Backend) setBackendStatus(ws, rs bool) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()
	b.status.Write = ws
	b.status.Read = rs
	b.status.Recovery = false
	b.status.UpdateTime = time.Now().Unix()
	b.metric.SetAlive("write", b.status.Write, flowLog)
	b.metric.SetAlive("read", b.status.Read, flowLog)
	flowLog.Debugf("setBackendStatus:%v,%v", ws, rs)
	flowLog.Tracef("done")
}

// Readable :
func (b *Backend) Readable() bool {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		b.statusLock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	flag := b.status.Read
	flowLog.Tracef("done")
	return flag
}

// IsWriteHealthy :
func (b *Backend) isWriteHealthy() bool {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		b.statusLock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	flowLog.Tracef("done")
	return b.status.Write
}

func (b *Backend) isInnerWriteHealthy() bool {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		b.statusLock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	flowLog.Tracef("done")
	return b.status.InnerWrite
}

func (b *Backend) setWriteHealthy(ws bool) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()

	b.status.Write = ws
	b.metric.SetAlive("write", b.status.Write, flowLog)
	flowLog.Debugf("setWriteHealthy:%v", ws)
	flowLog.Tracef("done")
}

func (b *Backend) setInnerWriteUnhealthy() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()
	b.status.InnerWrite = false
	b.metric.SetAlive("innerWrite", b.status.InnerWrite, flowLog)
	flowLog.Debugf("setInnerWriteHealthy:%v", false)
	flowLog.Tracef("done")
}

func (b *Backend) setReadUnhealthy() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()
	b.status.Read = false
	b.metric.SetAlive("read", b.status.Read, flowLog)
	flowLog.Debugf("setReadHealthy:%v", false)
	flowLog.Tracef("done")
}

func (b *Backend) notifyBackendStatus(ws, iws, rs bool) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()

	b.status.Write = ws
	b.status.InnerWrite = iws
	b.status.Read = rs
	b.status.Recovery = false
	b.status.UpdateTime = time.Now().Unix()
	flowLog.Debugf("notifyBackendStatus:%v,%v,%v", ws, iws, rs)
	// update metric status
	b.metric.SetAlive("read", b.status.Read, flowLog)
	b.metric.SetAlive("innerWrite", b.status.InnerWrite, flowLog)
	if b.status.InnerWrite {
		flowLog.Tracef("mark is up now")
	} else {
		flowLog.Tracef("mark is down now")
	}
	flowLog.Tracef("done")
}

func (b *Backend) resetInvalidCount() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()
	b.status.InvalidCount = 0
	flowLog.Tracef("done")
}

func (b *Backend) invalidCountInc() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.Lock()
	flowLog.Tracef("get Lock")
	defer func() {
		b.statusLock.Unlock()
		flowLog.Tracef("release Lock")
	}()
	b.status.InvalidCount++
	flowLog.Debugf("invalidCountInc")
	flowLog.Tracef("done")
}

func (b *Backend) getInvalidCount() int64 {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.statusLock.RLock()
	flowLog.Tracef("get RLock")
	defer func() {
		b.statusLock.RUnlock()
		flowLog.Tracef("release RLock")
	}()
	flowLog.Tracef("done")
	return b.status.InvalidCount
}
