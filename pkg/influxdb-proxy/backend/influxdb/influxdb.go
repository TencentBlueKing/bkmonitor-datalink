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
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// CountIncFunc :
type CountIncFunc func(db string, status string, flowLog *logging.Entry)

// CountAddFunc :
type CountAddFunc func(db string, status string, count float64, flowLog *logging.Entry)

// Client http.Client抽象接口
type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

// Backend :
type Backend struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	version    string

	statusChannel chan *backend.Status
	status        backend.Status
	wg            *sync.WaitGroup
	rwLock        *sync.RWMutex
	statusLock    *sync.RWMutex
	name          string
	domain        string
	port          int
	invalidCount  int
	closed        bool

	metric *Metrics

	// 是否禁用
	disabled bool
	// 备份次数
	backupCount int
	// 备份速率限制
	backupLimiter *rate.Limiter

	// 当返回码为500时，是否强制进行备份，true则备份
	wrongStatusBackup bool

	// backupStorage info
	backupStorage    StorageBackup
	backupCtx        context.Context
	backupCancelFunc context.CancelFunc

	// http transport
	client Client
	addr   string
	auth   backend.Auth

	// 写入缓冲区配置
	isBufferActivate   bool               // 是否启用缓存区标记位
	bufferMap          map[string]*Buffer // 缓存区集合
	bufferFlushTime    time.Duration      // 缓存区刷新周期
	bufferMaxPoints    int                // 缓存区阈值
	bufferWriteChannel chan *BufferData   // 缓冲区传递channel
	bufferFlushChannel chan *Buffer       // 缓冲区清空channel
	bufferFlushNotify  chan string        // 缓冲区清理信号
}

// NewBackend :
var NewBackend = func(ctx context.Context, config *backend.BasicConfig) (backend.Backend, chan *backend.Status, error) {
	metric := NewBackendMetric(config.Name)
	return NewInfluxDBFrontend(ctx, config, metric)
}

var moduleName = "influxdb"

// NewHTTPClient :
var NewHTTPClient = func(timeout time.Duration) Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 跳过证书验证
			},
		},
	}
}

// NewInfluxDBFrontend : client can only re-use but not share
func NewInfluxDBFrontend(rootCtx context.Context, config *backend.BasicConfig, metric *Metrics) (*Backend, chan *backend.Status, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": config.Name,
	})
	var err error
	ctx, cancelFunc := context.WithCancel(rootCtx)
	addr := fmt.Sprintf("http://%s:%d", config.Address, config.Port)
	if config.Protocol == "https" {
		addr = fmt.Sprintf("https://%s:%d", config.Address, config.Port)
	}
	// 认证信息
	auth := config.GetBasicAuth()

	bk := &Backend{
		ctx:           ctx,
		cancelFunc:    cancelFunc,
		statusChannel: make(chan *backend.Status, 16),
		domain:        config.Address,
		port:          config.Port,

		name:              config.Name,
		wrongStatusBackup: config.ForceBackup,
		// 是否可用开关
		disabled: config.Disabled,
		// 备份执行次数
		backupCount: 0,
		// 限速器，令牌桶，单位为 次/秒，桶容量为 1
		backupLimiter: rate.NewLimiter(rate.Limit(config.BackupRateLimit), 1),
		wg:            new(sync.WaitGroup),
		rwLock:        new(sync.RWMutex),
		statusLock:    new(sync.RWMutex),
		// 初始化状态下，后端应该是不可读不可写 ，直到发现ping可用方可使用
		status:           backend.Status{Read: false, Write: true, InnerWrite: false, UpdateTime: time.Now().Unix()},
		backupCancelFunc: nil,
		backupCtx:        nil,
		metric:           metric,
		addr:             addr,
		client:           NewHTTPClient(config.Timeout),
		auth:             auth,
	}

	// 检查kafka开关，如果开关关闭，则不启用kafka，使用一个空对象代替kafka操作
	if config.IgnoreKafka {
		bk.backupStorage = newEmptyKafka()
	} else {
		// make a backup storage
		if bk.backupStorage, err = NewKafkaBackup(ctx, config.Name); err != nil {
			cancelFunc()
			flowLog.Errorf("init kafka failed,error:%s", err)
			return nil, nil, backend.ErrInitKafkaBackup
		}
	}
	size, err := bk.backupStorage.GetOffsetSize()
	if err != nil {
		flowLog.Errorf("get kafka offset size failed,proxy will not init backup metric,error:%s", err)
	}
	bk.metric.SetBackupCount(float64(size), flowLog)

	// new goroutine to watch kafka data
	go func() {
		err := bk.init()
		if err != nil {
			flowLog.Errorf("init backend get error:%s", err)
		}
	}()

	// 根据配置，启动批次聚合buffer
	bk.initBuffer()

	return bk, bk.statusChannel, nil
}

func (b *Backend) initBuffer() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	// 获取backend的批次聚合发送时间
	flushTime, err := time.ParseDuration(common.Config.GetString(common.ConfigKeyFlushTime))
	if err != nil {
		flowLog.Errorf("failed to parse flush_time->[%s] config for->[%s] will use default 5s.",
			common.Config.GetString(common.ConfigKeyFlushTime), err)
	}

	// 获取backend的批次聚合发送阈值
	batchSize := common.Config.GetInt(common.ConfigKeyBatchSize)

	// 最大flush并发量，防止将后台写崩
	concurrency := common.Config.GetInt(common.ConfigKeyMaxFlushConcurrency)

	// 如果都有配置，则启动批次发送方案
	if flushTime != 0 && batchSize != 0 && concurrency != 0 {
		b.isBufferActivate = true
		b.bufferMap = make(map[string]*Buffer)
		b.bufferFlushTime = flushTime
		b.bufferMaxPoints = batchSize
		// 允许最终写入有一定延迟，利用缓冲区处理
		b.bufferWriteChannel = make(chan *BufferData, 10000)
		b.bufferFlushChannel = make(chan *Buffer)
		b.bufferFlushNotify = make(chan string)

		// 如果是有启动缓存区，需要goroutines负责处理数据
		// 单backend并发写入控制
		for i := 0; i < concurrency; i++ {
			b.wg.Add(1)
			go b.sendDataWithBuffer(i)
		}

		// 处理外部写入数据的线程
		b.wg.Add(1)
		go b.handleDataWithBuffer()
	}
}

func (b *Backend) sendWithBuffer(buffer *Buffer, metricFunc CountAddFunc) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":     moduleName,
		"backend":    b.Name(),
		"buffer_key": buffer.GetKey(),
	})
	defer func() {
		// 拦截异常，让外层循环能继续处理数据
		p := recover()
		if p != nil {
			flowLog.Errorf("send with buffer panic:%v", p)
			var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			flowLog.Errorf("panic stack ==> %s\n", buf[:n])
		}
	}()

	pointCount := buffer.GetPointCount()
	urlParams := buffer.GetWriteParams()
	db := urlParams.DB
	rp := urlParams.RP
	header := buffer.GetHeader()
	// 若实际实例不可写，则直接备份数据
	flowLog.Debugf("start to check isInnerWriteHealthy")
	if !b.isInnerWriteHealthy() {
		flowLog.Debugf("check inner write unhealthy ")
		err := b.writeBackup(0, urlParams, buffer, header, flowLog)
		if err != nil {
			flowLog.Errorf("flush buffer failed and backup failed,error:%s", err)
			metricFunc(db, "fail", float64(pointCount), flowLog)
			return
		}
		metricFunc(db, "backup", float64(pointCount), flowLog)
		return
	}
	resp, err := b.sendWriteRequest(urlParams, buffer, header, flowLog)
	// 若写入报错，和上面一样要进行kafka备份以及错误信息记录
	if err != nil {
		flowLog.Errorf("write get error")
		// 如果是网络错误则备份
		if err == backend.ErrNetwork {
			flowLog.Errorf("flush buffer to influxdb failed,error:%s", err)
			// reader被上面sendWriteRequest读取过了，需要重置一下
			buffer.SeekZero()
			err = b.writeBackup(0, urlParams, buffer, header, flowLog)
			if err != nil {
				flowLog.Errorf("write failed and backup failed,error:%s", err)
				metricFunc(db, "fail", float64(pointCount), flowLog)
				return
			}
			metricFunc(db, "backup", float64(pointCount), flowLog)
			return
		}
		// 否则直接以失败状态返回
		flowLog.Errorf("Error: %s not network Error, nothing will push to database.", err)
		metricFunc(db, "fail", float64(pointCount), flowLog)
		return
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			flowLog.Errorf("close body failed,error:%s", err)
		}
	}()
	flowLog.Debugf("read response body")
	// 读取返回结果,influxdb的内部错误不会作为urlerror返回，所以需要独立进行记录
	resBuf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		flowLog.Errorf("get error while read write response,error:%s", err)
		// 初始化一下，防止空指针异常
		resBuf = []byte("read body failed")
	}
	responseStr := string(resBuf)
	respCode := resp.StatusCode
	// 如果wrongStatusBackup开关打开，则备份返回错误码500以上的数据
	if b.shouldBackup(respCode) {
		flowLog.Errorf("write to influx db get wrong resp, db: %s, rp: %s, status code: %d, reason: %s, try to backup", db, rp, respCode, responseStr)
		// reader被上面sendWriteRequest读取过了，需要重置一下
		buffer.SeekZero()
		err = b.writeBackup(0, urlParams, buffer, header, flowLog)
		if err != nil {
			flowLog.Errorf("flush buffer failed and backup failed,error:%s", err)
			metricFunc(db, "fail", float64(pointCount), flowLog)
			return
		}
		metricFunc(db, "backup", float64(pointCount), flowLog)
		return
	}
	// 300以上的code应该记录下来
	if respCode >= 300 {
		flowLog.Warnf("get wrong status code:%d,result:%s", respCode, responseStr)
	}
	flowLog.Debugf("flush buffer success with response:%s,status code:%d", resBuf, respCode)
	metricFunc(db, "success", float64(pointCount), flowLog)
	flowLog.Debugf("done")
}

func (b *Backend) sendDataWithBuffer(index int) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	defer flowLog.Infof("index:%d loop send buffer exit", index)
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case buffer := <-b.bufferFlushChannel:
			// 获取到buffer后，启动线程准备发送数据
			b.sendWithBuffer(buffer, b.metric.FlushCountAdd)
		}
	}
}

func (b *Backend) handleWriteBuffer(bufferData *BufferData, flowLog *logging.Entry) {
	defer func() {
		// 拦截异常，让外层循环能继续处理数据
		p := recover()
		if p != nil {
			flowLog.Errorf("send with buffer panic:%v", p)
			var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			flowLog.Errorf("panic stack ==> %s\n", buf[:n])
		}
	}()
	// 获取写入的数据并加入缓冲区
	b.metric.BufferCountAdd(bufferData.URLParams.DB, "received", float64(bufferData.Reader.PointCount()), flowLog)
	key := fmt.Sprintf("%s:%s", b.Name(), bufferData.URLParams.DB)
	// 如果 rp 存在，则 buffer key 中添加上 rp 信息
	if bufferData.URLParams.RP != "" {
		key = fmt.Sprintf("%s:%s:%s", b.Name(), bufferData.URLParams.DB, bufferData.URLParams.RP)
	}
	// 如果没有buffer就新增一个
	buffer, ok := b.bufferMap[key]
	if !ok {
		buffer = NewBuffer(b.ctx, key, bufferData.URLParams, bufferData.Header, b.bufferFlushTime, b.bufferFlushNotify)
		b.bufferMap[key] = buffer
	}
	// 将新数据加入
	buffer.AddReader(bufferData.Flow, bufferData.Reader)
	b.metric.BufferCountAdd(bufferData.URLParams.DB, "success", float64(bufferData.Reader.PointCount()), flowLog)
	// 如果buffer已经缓冲了预设数量的数据，则将其发送
	if buffer.GetPointCount() >= b.bufferMaxPoints {
		flowLog.Infof("buffer key:%s reach max length,start flush", key)
		buffer.TimerStop()
		b.bufferFlushChannel <- buffer
		delete(b.bufferMap, key)
	}
}

func (b *Backend) handleBufferNotify(key string, flowLog *logging.Entry) {
	defer func() {
		// 拦截异常，让外层循环能继续处理数据
		p := recover()
		if p != nil {
			flowLog.Errorf("send with buffer panic:%v", p)
			var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			flowLog.Errorf("panic stack ==> %s\n", buf[:n])
		}
	}()
	flowLog.Infof("buffer key:%s timeout,start flush", key)
	// 获取buffer，将其发送并从map中清除
	buffer, ok := b.bufferMap[key]
	if !ok {
		flowLog.Errorf("missing buffer by key:%s,something wrong?", key)
		return
	}
	buffer.TimerStop()
	b.bufferFlushChannel <- buffer
	delete(b.bufferMap, key)
}

func (b *Backend) handleDataWithBuffer() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	defer flowLog.Infof("loop handle buffer exit")
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case bufferData := <-b.bufferWriteChannel:
			b.handleWriteBuffer(bufferData, flowLog)
		case key := <-b.bufferFlushNotify:
			b.handleBufferNotify(key, flowLog)
		}
	}
}

// Reset 重置参数,目前设置的是传入consul.Host
func (b *Backend) Reset(config *backend.BasicConfig) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	b.rwLock.Lock()
	flowLog.Debugf("get Lock")
	defer func() {
		b.rwLock.Unlock()
		flowLog.Debugf("release Lock")
	}()

	flowLog.Debugf("start reset")
	addr := fmt.Sprintf("http://%s:%d", config.Address, config.Port)
	if config.Protocol == "https" {
		addr = fmt.Sprintf("https://%s:%d", config.Address, config.Port)
	}
	b.domain = config.Address
	b.port = config.Port
	b.addr = addr
	b.auth = config.Auth
	b.disabled = config.Disabled
	b.backupLimiter.SetLimit(rate.Limit(config.BackupRateLimit))
	// 初始化信息后，应更新一次状态数据
	go b.checkInfluxDBStatus()
	flowLog.Debugf("Reset finished,target address:%s,status:%s", b.addr, b.status)
	flowLog.Debugf("done")
	return nil
}

func (b *Backend) BackupCountInc() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	b.rwLock.Lock()
	flowLog.Debugf("get Lock")
	defer func() {
		b.rwLock.Unlock()
		flowLog.Debugf("release Lock")
	}()
	flowLog.Debugf("start reset")
	b.backupCount += 1
	flowLog.Debugf("backup count inc, %d", b.backupCount)
	flowLog.Debugf("done")
	return nil
}

// Wait :
func (b *Backend) Wait() {
	b.wg.Wait()
}

// Close : close backend, should call Wait() function to wait
func (b *Backend) Close() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	b.rwLock.Lock()
	flowLog.Debugf("get Lock")
	defer func() {
		b.rwLock.Unlock()
		flowLog.Debugf("release Lock")
	}()
	if b.closed {
		flowLog.Warnf("already closed,will not do it again")
		return nil
	}
	b.cancelFunc()
	b.closed = true
	flowLog.Debugf("done")
	return nil
}

// String : 返回唯一字符描述标识
func (b *Backend) String() string {
	return fmt.Sprintf("influxdb_backend[%s:%s:%d-%s]disabled[%v]backup_rate_limit[%v]", b.name, b.domain, b.port, b.addr, b.disabled, b.backupLimiter.Limit())
}

// 写入失败后的数据备份
func (b *Backend) backupData(flow uint64, urlParams *backend.WriteParams, points []byte, header http.Header) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	metricFunc := b.metric.BackupCountInc
	metricFunc(db, "received", flowLog)
	// b.metric.BackupStartCountInc(db, flowLog)

	// 如果备份关闭则直接丢弃
	limiter := b.BackupLimiter()
	if limiter.Limit() < 0 {
		metricFunc(db, "dropped", flowLog)
		return nil
	}

	var err error
	var buffer *bytes.Buffer
	// 如果打包数据出错，只能证明数据格式有问题，此处直接报错返回
	if buffer, err = backupDataToBuffer(flow, urlParams, string(points), header); err != nil {
		flowLog.Errorf("failed to trans data to backend data for->[%s]", err)
		metricFunc(db, "fail", flowLog)
		// b.metric.BackupFailedCountInc(db, flowLog)
		return backend.ErrBackupDataToBuffer
	}
	flowLog.Debugf("backend influx write status unhealthy, push db %s data to kafka directly", db)
	// 此处推送备份数据到kafka，如果出错，则记录kafka可用为false
	err = b.backupStorage.Push(string(buffer.Bytes()))
	if err != nil {
		flowLog.Debugf("push backup data get error:%s", err)
		// kafka healthy -> unhealthy, kafka invalid
		if b.isWriteHealthy() {
			// notify write invalid event, and set write status false
			b.invalidCountInc()
			// b.notifyBackendStatus(false, false, false)
			flowLog.Errorf("kafka invalid, set backend write inner_write read status false")
		}
		metricFunc(db, "fail", flowLog)
		// b.metric.BackupFailedCountInc(db, flowLog)
		return backend.ErrPushDataToKafka
	}
	flowLog.Debugf("backup data done")
	// err为空则证明kafka数据备份成功
	metricFunc(db, "success", flowLog)
	// b.metric.BackupSuccessCountInc(db, flowLog)

	flowLog.Debugf("done")
	return nil
}

func (b *Backend) sendWriteRequest(urlParams *backend.WriteParams, reader io.Reader, header http.Header, flowLog *logging.Entry) (*http.Response, error) {
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	value := url.Values{}
	value.Set("db", db)
	consistency := urlParams.Consistency
	if consistency != "" {
		value.Set("consistency", consistency)
	}
	precision := urlParams.Precision
	if precision != "" {
		value.Set("precision", precision)
	}
	rp := urlParams.RP
	if rp != "" {
		value.Set("rp", rp)
	}
	flowLog.Debugf("combine url params done")
	req, err := http.NewRequest("POST", b.addr+"/write?"+value.Encode(), reader)
	if err != nil {
		flowLog.Errorf("NewRequest failed:%s", err)
		return nil, backend.ErrInitRequest
	}
	// 复制必要的头部信息
	backend.CopyHeader(req.Header, header)
	flowLog.Debugf("copy header done")
	err = b.auth.SetAuth(req)
	if err != nil {
		flowLog.Errorf("authorization header set failed,but still send message to backend,error:%s", err)
	}
	flowLog.Debugf("start write")
	resp, err := b.client.Do(req)
	if err != nil {
		flowLog.Errorf("do write failed,error:%s", err)
		// 针对URL错误进行特殊处理，如果是URL错误则返回netowrk错误，否则返回写入错误
		if _, ok := err.(*url.Error); ok {
			flowLog.Errorf("get network error:%s", err)
			return nil, backend.ErrNetwork
		}
		return nil, backend.ErrDoWrite
	}
	return resp, nil
}

// Write : 写入数据
func (b *Backend) Write(flow uint64, urlParams *backend.WriteParams, reader backend.CopyReader, header http.Header) (*backend.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})

	if b.isBufferActivate {
		b.metric.WriteCountInc(urlParams.DB, "received", flowLog)
		resp, err := b.writeWithBuffer(flow, urlParams, reader, header, b.metric.BufferCountAdd)
		if err != nil {
			b.metric.WriteCountInc(urlParams.DB, "failed", flowLog)
			return nil, err
		}
		// 缓存模式下，写入请求直接判断为成功
		b.metric.WriteCountInc(urlParams.DB, "buffered", flowLog)
		return resp, nil
	}
	return b.writeWithMetric(flow, urlParams, reader, header, b.metric.WriteCountInc)
}

func (b *Backend) writeWithBuffer(flow uint64, urlParams *backend.WriteParams, reader backend.CopyReader, header http.Header, metricFunc CountAddFunc) (*backend.Response, error) {
	// 使用buffer，则所有请求都返回204
	resp := backend.NewResponse("", 204)
	b.bufferWriteChannel <- NewBufferData(flow, urlParams, header, reader)
	return resp, nil
}

// 根据返回的结果判断是否应该备份数据
// 如果wrongStatusBackup开关打开，则备份code 500以上，否则不启动备份
func (b *Backend) shouldBackup(code int) bool {
	if b.wrongStatusBackup {
		if code >= 500 && code < 600 {
			return true
		}
	}
	return false
}

func (b *Backend) writeBackup(flow uint64, urlParams *backend.WriteParams, reader io.Reader, header http.Header, flowLog *logging.Entry) error {
	// the error from influx db client is network error, because influx db
	// inner error will in the 200 result, eg. {"results":[{"statement_id":0,"error":"database not found: mydb"}]}
	data, err := io.ReadAll(reader)
	if err != nil {
		flowLog.Errorf("backupData failed,for ReadAll get error,error:%s", err)
		return backend.ErrReadReader
	}
	// 下面进行备份操作,备份失败视为写入失败，但是备份成功则视为写入成功
	err = b.backupData(flow, urlParams, data, header)
	if err != nil {
		flowLog.Errorf("backupData failed,error:%s", err)
		return backend.ErrBackupData
	}
	flowLog.Debugf("write failed but backup success")
	return nil
}

func (b *Backend) writeWithMetric(flow uint64, urlParams *backend.WriteParams, reader backend.CopyReader, header http.Header, metricFunc CountIncFunc) (*backend.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	rp := urlParams.RP
	b.rwLock.RLock()
	flowLog.Debugf("get Rlock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release Rlock")
	}()
	metricFunc(db, "received", flowLog)
	// b.metric.WriteReceivedCountInc(db, flowLog)
	// check influx db write healthy or not
	// write unhealthy, push data to kafka directly
	flowLog.Debugf("get db")
	// 若实际实例不可写，则直接备份数据
	flowLog.Debugf("start to check isInnerWriteHealthy")
	if !b.isInnerWriteHealthy() {
		flowLog.Debugf("check inner write unhealthy ")
		err := b.writeBackup(flow, urlParams, reader, header, flowLog)
		if err != nil {
			flowLog.Errorf("write failed and backup failed,error:%s", err)
			metricFunc(db, "fail", flowLog)
			return nil, backend.ErrWriteBackup
		}
		metricFunc(db, "backup", flowLog)
		return backend.NewResponse(writeSuccStr, succesWithoutResult), nil
	}
	flowLog.Debugf("check  isInnerWriteHealthy done")

	resp, err := b.sendWriteRequest(urlParams, reader, header, flowLog)
	// 若写入报错，和上面一样要进行kafka备份以及错误信息记录
	if err != nil {
		flowLog.Errorf("write get error")
		// 如果是网络错误则备份
		if err == backend.ErrNetwork {
			flowLog.Errorf("write to influx db failed,error:%s", err)
			// reader被上面sendWriteRequest读取过了，需要重置一下
			reader.SeekZero()
			err = b.writeBackup(flow, urlParams, reader, header, flowLog)
			if err != nil {
				flowLog.Errorf("write failed and backup failed,error:%s", err)
				metricFunc(db, "fail", flowLog)
				return nil, backend.ErrWriteBackup
			}
			metricFunc(db, "backup", flowLog)
			return backend.NewResponse(writeSuccStr, succesWithoutResult), nil
		}
		// 否则直接以失败状态返回
		flowLog.Errorf("Error: %s not network Error, nothing will push to database.", err)
		metricFunc(db, "fail", flowLog)
		return nil, err

	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			flowLog.Errorf("close body failed,error:%s", err)
		}
	}()
	flowLog.Debugf("read response body")
	// 读取返回结果,influxdb的内部错误不会作为urlerror返回，所以需要独立进行记录
	resBuf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		flowLog.Errorf("get error while read write response,error:%s", err)
		// 初始化一下，防止空指针异常
		resBuf = []byte("read body failed")
		// return string(resBuf), resp.StatusCode, nil
	}
	responseStr := string(resBuf)
	respCode := resp.StatusCode
	// 如果wrongStatusBackup开关打开，则备份返回错误码500以上的数据
	if b.shouldBackup(respCode) {
		flowLog.Errorf("write to influx db get wrong resp, db: %s, rp: %s, status code: %d, reason: %s, try to backup", db, rp, respCode, responseStr)
		// reader被上面sendWriteRequest读取过了，需要重置一下
		reader.SeekZero()
		err = b.writeBackup(flow, urlParams, reader, header, flowLog)
		if err != nil {
			flowLog.Errorf("write failed and backup failed,error:%s", err)
			metricFunc(db, "fail", flowLog)
			return nil, backend.ErrWriteBackup
		}
		metricFunc(db, "backup", flowLog)
		return backend.NewResponse(writeSuccStr, succesWithoutResult), nil
	}
	// 300以上的code应该记录下来
	if respCode >= 300 {
		flowLog.Warnf("get wrong status code:%d,result:%s", respCode, responseStr)
	}
	flowLog.Debugf("write success with response:%s,status code:%d", resBuf, respCode)
	metricFunc(db, "success", flowLog)
	flowLog.Debugf("done")
	return backend.NewResponse(responseStr, respCode), nil
}

// RawQuery :
func (b *Backend) RawQuery(flow uint64, request *http.Request) (*http.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	req, err := http.NewRequest("POST", b.addr+"/api/v2/query", request.Body)
	req.Header = request.Header
	b.metric.QueryFluxCountInc(req.Header.Get("db"), "received", flowLog)
	err = b.auth.SetAuth(req)
	if err != nil {
		b.metric.QueryFluxCountInc(req.Header.Get("db"), "failed", flowLog)
		flowLog.Errorf("set auth failed,error:%s", err)
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.metric.QueryFluxCountInc(req.Header.Get("db"), "failed", flowLog)
		flowLog.Errorf("do raw query failed,error:%s", err)
		if _, ok := err.(*url.Error); ok {
			b.invalidCountInc()
			b.setReadUnhealthy()
			b.sendStatusEvent()
			flowLog.Errorf("notify backend read false status event")
			return nil, backend.ErrNetwork
		}
		return nil, err
	}
	b.metric.QueryFluxCountInc(req.Header.Get("db"), "success", flowLog)
	return resp, nil
}

// CreateDatabase和Query都会用到这个方法，为防止读锁重入，所以将其从Query逻辑中分离
func (b *Backend) query(flow uint64, urlParams *backend.QueryParams, header http.Header) (*http.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	sql := urlParams.SQL
	epoch := urlParams.Epoch
	pretty := urlParams.Pretty
	chunked := urlParams.Chunked
	chunkSize := urlParams.ChunkSize
	var err error

	// 填充透传过来的url参数
	subURL := url.Values{}
	subURL.Set("q", sql)
	subURL.Set("db", db)
	if epoch != "" {
		subURL.Set("epoch", epoch)
	}
	if pretty != "" {
		subURL.Set("pretty", pretty)
	}
	if chunked != "" {
		subURL.Set("chunked", chunked)
	}
	if chunkSize != "" {
		subURL.Set("chunk_size", chunkSize)
	}

	flowLog.Debugf("combine url params done")
	request, err := http.NewRequest("POST", b.addr+"/query?"+subURL.Encode(), nil)
	if err != nil || request == nil {
		flowLog.Errorf("internal url parse error:%s", err)
		return nil, backend.ErrInitRequest
	}
	flowLog.Debugf("new request done")
	// 复制必要的头部信息
	backend.CopyHeader(request.Header, header)
	err = b.auth.SetAuth(request)
	if err != nil {
		flowLog.Errorf("authorization header set failed,but still send message to backend,error:%s", err)
	}
	flowLog.Debugf("start query request")
	resp, err := b.client.Do(request)
	if err != nil {
		flowLog.Errorf("Query db [%s] sql [%s] failed,error:%s", db, sql, err)
		// only notify read false if error cause by network error
		// url error证明与实例的连通有问题
		if _, ok := err.(*url.Error); ok {
			b.invalidCountInc()
			b.setReadUnhealthy()
			b.sendStatusEvent()
			flowLog.Errorf("notify backend read false status event")
			return nil, backend.ErrNetwork
		}
		return nil, backend.ErrDoQuery
	}
	flowLog.Debugf("query request done")
	return resp, nil
}

// Query : 读取数据
func (b *Backend) Query(flow uint64, urlParams *backend.QueryParams, header http.Header) (*backend.Response, error) {
	return b.queryWithMetric(flow, urlParams, header, b.metric.QueryCountInc)
}

func (b *Backend) queryWithMetric(flow uint64, urlParams *backend.QueryParams, header http.Header, metricFunc CountIncFunc) (*backend.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	sql := urlParams.SQL
	b.rwLock.RLock()
	flowLog.Debugf("get query RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release query RLock")
	}()
	metricFunc(db, "received", flowLog)
	flowLog.Debugf("start query")
	resp, err := b.query(flow, urlParams, header)
	if err != nil {
		flowLog.Errorf("query error:%s", err)
		metricFunc(db, "fail", flowLog)
		return nil, err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			flowLog.Errorf("close body failed,error:%s", err)
		}
	}()
	flowLog.Debugf("read response body")
	content, err := ioutil.ReadAll(resp.Body)
	// 查询body如果有error，则查询失败
	if err != nil {
		flowLog.Errorf("read body error:%s,the query is %s", err, sql)
		metricFunc(db, "fail", flowLog)
		return nil, backend.ErrReadBody
	}

	contentStr := string(content)
	code := resp.StatusCode
	if code >= 300 {
		flowLog.Warnf("get wrong status code:%d,result:%s", code, contentStr)
	}
	metricFunc(db, "success", flowLog)
	flowLog.Debugf("query db->[%s] sql->[%s] with result->[%#v] content->[%s]", db, sql, resp, contentStr)
	flowLog.Debugf("done")
	return backend.NewResponse(contentStr, code), nil
}

// CreateDatabase : 创建数据库，传入q而非DB名，是为了防止语句有复杂的配置需要解析
func (b *Backend) CreateDatabase(flow uint64, urlParams *backend.QueryParams, header http.Header) (*backend.Response, error) {
	return b.createDatabaseWithMetric(flow, urlParams, header, b.metric.CreateDBCountInc)
}

func (b *Backend) createDatabaseWithMetric(flow uint64, urlParams *backend.QueryParams, header http.Header, metricFunc CountIncFunc) (*backend.Response, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"backend": b.Name(),
	})
	flowLog.Tracef("called,urlParams:%s", urlParams)
	db := urlParams.DB
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	metricFunc(db, "received", flowLog)
	sql := urlParams.SQL
	// 如果实例不可写，则备份
	flowLog.Debugf("check isInnerWriteHealthy")
	if !b.isInnerWriteHealthy() {
		// 下面进行备份操作
		err := b.backupData(flow, backend.NewWriteParams("", "", "", ""), []byte(sql), header)
		if err != nil {
			flowLog.Errorf("backupData failed,error:%s", err)
			metricFunc(db, "fail", flowLog)
			return nil, backend.ErrBackupData
		}
		metricFunc(db, "backup", flowLog)
		return backend.NewResponse(createDBsuccStr, successOrBackup), nil
	}

	flowLog.Debugf("start createdb query")
	resp, err := b.query(flow, urlParams, header)
	if err != nil {
		flowLog.Errorf("createdb get error")
		// 网络错误则备份数据
		if err == backend.ErrNetwork {
			// 下面进行备份操作
			err := b.backupData(flow, backend.NewWriteParams("", "", "", ""), []byte(sql), header)
			if err != nil {
				flowLog.Errorf("backupData failed,error:%s", err)
				metricFunc(db, "fail", flowLog)
				return nil, backend.ErrBackupData
			}
			metricFunc(db, "backup", flowLog)
			flowLog.Errorf("sql %s failed:%s", sql, err)
			// return the standard json format
			// result := "{\"results\":[{\"series\":null}]}"
			return backend.NewResponse(createDBsuccStr, successOrBackup), nil
		}
		// 否则直接以失败处理
		flowLog.Errorf("create database content error,error:%s", err)
		metricFunc(db, "fail", flowLog)
		// result := "create database failed for content error"
		return nil, err
	}
	flowLog.Debugf("read response body")
	// 读取返回结果,influxdb的内部错误不会作为urlerror返回，所以需要独立进行记录
	resBuf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		flowLog.Errorf("get error while read write response,error:%s", err)
		// 初始化一下，防止空指针异常
		resBuf = []byte("read body failed")
		// return string(resBuf), resp.StatusCode, nil
	}
	responseStr := string(resBuf)
	respCode := resp.StatusCode
	// 如果wrongStatusBackup开关打开，则备份返回错误码500以上的数据
	if b.shouldBackup(respCode) {
		flowLog.Errorf("createdb to influx db get wrong resp statuscode:%d,reason:%s,try to backup", respCode, responseStr)
		err := b.backupData(flow, backend.NewWriteParams("", "", "", ""), []byte(sql), header)
		if err != nil {
			flowLog.Errorf("write failed and backup failed,error:%s", err)
			metricFunc(db, "fail", flowLog)
			return nil, backend.ErrBackupData
		}
		metricFunc(db, "backup", flowLog)
		return backend.NewResponse(createDBsuccStr, successOrBackup), nil
	}
	// 300以上的错误记录下来
	if respCode >= 300 {
		flowLog.Warnf("get wrong status code:%d,result:%s", respCode, responseStr)
	}
	flowLog.Debugf("write success with response:%s", resBuf)
	metricFunc(db, "success", flowLog)
	flowLog.Debugf("done")
	return backend.NewResponse(responseStr, respCode), nil
}

// GetVersion : 获取influxDB版本号
func (b *Backend) GetVersion() string {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	return b.version
}

func (b *Backend) updateVersion() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	cost, version, err := b.Ping(maxPingTime * time.Second)
	if err != nil {
		flowLog.Errorf("GetVersion failed:%s, cost time:%s", err, cost)
	}
	b.version = version
}

// Ping :
func (b *Backend) Ping(timeout time.Duration) (time.Duration, string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Debugf("called")
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	now := time.Now()
	req, err := http.NewRequest("GET", b.addr+"/ping", nil)
	if err != nil {
		return 0, "", err
	}
	if timeout > 0 {
		params := req.URL.Query()
		params.Set("wait_for_leader", fmt.Sprintf("%.0fs", timeout.Seconds()))
		req.URL.RawQuery = params.Encode()
	}

	resp, err := b.client.Do(req)
	if err != nil {
		flowLog.Errorf("do ping failed,error:%s", err)
		return 0, "", err
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			flowLog.Errorf("close body failed,error:%s", err)
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		flowLog.Errorf("read all failed,error:%s", err)
		return 0, "", err
	}
	if resp.StatusCode != http.StatusNoContent {
		flowLog.Errorf("ping result code not as expected,code:%d", resp.StatusCode)
		err := errors.New(string(body))
		return 0, "", err
	}

	version := resp.Header.Get("X-Influxdb-Version")
	flowLog.Debugf("done")
	return time.Since(now), version, nil
}

// Name :
func (b *Backend) Name() string {
	return b.name
}

// Disabled :
func (b *Backend) Disabled() bool {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	flag := b.disabled
	b.metric.SetAlive("status", b.disabled == false, flowLog)
	flowLog.Tracef("done")
	return flag
}

// BackupCount :
func (b *Backend) BackupCount() int {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	flag := b.backupCount
	flowLog.Tracef("done")
	return flag
}

// BackupLimiter :
func (b *Backend) BackupLimiter() *rate.Limiter {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	limiter := b.backupLimiter
	flowLog.Tracef("done")
	return limiter
}

// SetBackupLimiter :
func (b *Backend) SetBackupLimiter() *rate.Limiter {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"backend": b.Name(),
	})
	flowLog.Tracef("called")
	b.rwLock.RLock()
	flowLog.Debugf("get RLock")
	defer func() {
		b.rwLock.RUnlock()
		flowLog.Debugf("release RLock")
	}()
	limiter := b.backupLimiter
	flowLog.Tracef("done")
	return limiter
}

func init() {
	backend.RegisterBackend("influxdb", NewBackend)
}
