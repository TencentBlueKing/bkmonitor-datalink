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
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// BufferData :
type BufferData struct {
	Flow      uint64
	URLParams *backend.WriteParams
	Header    http.Header
	Reader    backend.CopyReader
}

// NewBufferData :
func NewBufferData(flow uint64, urlParams *backend.WriteParams, header http.Header, reader backend.CopyReader) *BufferData {
	return &BufferData{flow, urlParams, header, reader}
}

// Buffer 用于做缓存操作，集齐缓存数据后统一写入到influxdb中
// 为了换取开发的方便，所以在写入触发的时候，将会将所有的数据读取到buffer的缓存中，再逐一返回到外部
type Buffer struct {
	ctx    context.Context
	cancel context.CancelFunc
	key    string

	// reader 相关
	readers     []backend.CopyReader // reader缓冲区，用于记录当前没有写入的reader
	buffer      []byte               // 字符串缓存区
	bufferIndex int                  // 当前缓存区读取的索引值

	// 通知机制及最大值相关
	pointCount   int         // 记录当前有多少的点在缓存区中
	timeoutTimer *time.Timer // 缓存区大小刷新的闹钟
	notify       chan string // 通知外部进行缓存写入的渠道

	urlParams *backend.WriteParams
	header    http.Header
}

// NewBuffer :
func NewBuffer(ctx context.Context, key string, urlParams *backend.WriteParams, header http.Header, timeout time.Duration, notify chan string) *Buffer {
	buffer := &Buffer{
		key:          key,
		urlParams:    urlParams,
		header:       header,
		readers:      make([]backend.CopyReader, 0),
		timeoutTimer: time.NewTimer(timeout),
		buffer:       make([]byte, 0, 1024*1024),
		notify:       notify,
	}
	buffer.ctx, buffer.cancel = context.WithCancel(ctx)
	// 启动周期notify
	go buffer.timeoutWatcher()
	return buffer
}

func (b *Buffer) GetWriteParams() *backend.WriteParams {
	return b.urlParams
}

func (b *Buffer) GetHeader() http.Header {
	return b.header
}

func (b *Buffer) GetKey() string {
	return b.key
}

func (b *Buffer) GetPointCount() int {
	return b.pointCount
}

func (b *Buffer) SeekZero() {
	b.bufferIndex = 0
}

// 从reader列表中读取所有的内容，并填充到buffer缓冲区中
func (b *Buffer) fillBuffer() error {
	log := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"key":    b.GetKey(),
	})

	for index, reader := range b.readers {
		log.Debugf("ready to read the index->[%d] reader total->[%d]", index, len(b.readers))

		tempBuffer, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Errorf("index->[%d] data get read error:%s", index, err)
			// 读取错误说明reader有问题，这时不应该进行写入，避免出现表名爆炸等问题
			return err
		}
		b.buffer = append(b.buffer, tempBuffer...)
	}
	// 填充之后清空reader，避免reader重复叠加留在内存里
	b.readers = make([]backend.CopyReader, 0)
	b.pointCount = 0
	log.Debugf("buffer filled.")
	return nil
}

func (b *Buffer) TimerStop() {
	b.cancel()
	b.timeoutTimer.Stop()
}

// 读取操作，用于读取所有缓存区中的内容
// 注意：一定需要先调用LockBuffer后，方可调用Read方法
// 一次完整的读取流程: LockBuffer-> Read*n -> ReleaseBuffer
func (b *Buffer) Read(p []byte) (int, error) {
	var (
		log = logging.NewEntry(map[string]interface{}{
			"module": moduleName,
			"key":    b.GetKey(),
		})
		remainLength int
	)

	// 判断如果缓存区为空，先读取所有的内容到本地
	if len(b.buffer) == 0 {
		log.Infof("empty buffer, will fill it first")
		err := b.fillBuffer()
		if err != nil {
			return 0, err
		}
	}

	// 按照提供的缓存大小，返回内容
	// 1. 获取当前剩余需要读取的内容长度
	remainLength = len(b.buffer) - b.bufferIndex

	// 2. 判断当前提供的缓冲区和需要读取的内容长度，
	if remainLength <= len(p) {
		// 如果可以一次性读取，则直接全部返回
		copy(p, b.buffer[b.bufferIndex:b.bufferIndex+remainLength])
		return remainLength, io.EOF
	}
	// 否则读取提供部分数据
	copy(p, b.buffer[b.bufferIndex:b.bufferIndex+len(p)])
	b.bufferIndex += len(p)

	return len(p), nil
}

// AddReader :
func (b *Buffer) AddReader(flow uint64, reader backend.CopyReader) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
		"key":     b.GetKey(),
	})

	b.readers = append(b.readers, reader)
	b.pointCount += reader.PointCount()
	flowLog.Infof("reader add, now has pointCount->[%d]", b.pointCount)
}

// Notify 通知外部要处理该buffer了
func (b *Buffer) Notify() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"key":    b.GetKey(),
	})

	select {
	case b.notify <- b.key:
		flowLog.Infof("notify signal sent.")
	case <-b.ctx.Done():
		flowLog.Infof("get ctx done when try to notify")
	}
}

func (b *Buffer) timeoutWatcher() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"key":    b.GetKey(),
	})

	for {
		select {
		case <-b.timeoutTimer.C:
			flowLog.Infof("clock alarm, will try to send notify")
			b.Notify()
		case <-b.ctx.Done():
			flowLog.Infof("timeout watcher exit")
			return
		}
	}
}
