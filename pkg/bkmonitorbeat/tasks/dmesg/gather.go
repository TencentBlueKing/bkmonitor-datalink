// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dmesg

import (
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type KmsgException struct {
	Name    string
	Message string
}

// 按序遍历异常判断列表
var kmsgExceptions = []KmsgException{
	{Name: "ext3_fs_error", Message: "EXT3-fs error"},
	{Name: "disk_io_error", Message: "I/O error"},
	{Name: "table_full_drop_pkg", Message: "table full, dropping packet"},
	{Name: "out_of_socket_mem", Message: "Out of socket memory"},
	{Name: "allocation_failed", Message: "allocation failed"},
	{Name: "neighbour_table_overf", Message: "neighbour table overflow"},
	{Name: "mce_error", Message: "^MCE"},
	{Name: "run_in_m_clock_mode", Message: "Running in modulated clock mode"},
	{Name: "transmit_time_out", Message: "transmit timed out"},
	{Name: "oom", Message: "(Out of Memory|out_of_memory|oom-kill)"},
	{Name: "alloc_kernel_sgl", Message: "Failed to alloc kernel SGL buffer for IOCTL"},
	{Name: "nmi_received", Message: "Uhhuh. NMI received"},
	{Name: "page_alloc_fail", Message: "page allocation failure"},
	{Name: "nic_link_change", Message: "NIC Link is"},
}

type Gather struct {
	tasks.BaseTask

	ctx    context.Context
	cancel context.CancelFunc
	parser *parser
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	p, err := newParser()
	if err != nil {
		logger.Errorf("failed to create dmseg parser: %v", err)
		return &Gather{}
	}

	gather.parser = p
	return gather
}

type expTime struct {
	Message string
	Time    time.Time
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	g.ctx, g.cancel = context.WithCancel(ctx)
	exps := make(map[string]expTime) // 相同事件在同一个上报周期内需要去重
	const batchSize = 10

	toWrapException := func(m map[string]expTime) []wrapException {
		var ret []wrapException
		for name, v := range m {
			ret = append(ret, wrapException{
				Name:    name,
				Message: v.Message,
				Time:    v.Time,
			})
		}
		return ret
	}

	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()

	ch := g.parser.Tail()
	defer g.parser.Close() // 退出前必须关闭句柄

	for {
		select {
		case <-g.ctx.Done():
			return

		case <-ticker.C:
			if len(exps) > 0 {
				e <- newEvent(g.TaskConfig.GetDataID(), toWrapException(exps))
				exps = make(map[string]expTime)
			}

		case exception := <-ch:
			exps[exception.Name] = expTime{
				Message: exception.Message,
				Time:    time.Now(),
			}
			if len(exps) >= batchSize {
				e <- newEvent(g.TaskConfig.GetDataID(), toWrapException(exps))
				exps = make(map[string]expTime)
			}
		}
	}
}

func (g *Gather) Stop() {
	g.cancel()
}

type parser struct {
	f         *os.File
	exception chan KmsgException
}

func newParser() (*parser, error) {
	f, err := os.Open("/dev/kmsg")
	if err != nil {
		return nil, err
	}

	_, err = f.Seek(0, os.SEEK_END)
	if err != nil {
		return nil, err
	}

	return &parser{
		f:         f,
		exception: make(chan KmsgException, 1),
	}, nil
}

func (p *parser) Tail() <-chan KmsgException {
	go func() {
		msg := make([]byte, 8192)
		for {
			// Each read call gives us one full message.
			// https://www.kernel.org/doc/Documentation/ABI/testing/dev-kmsg
			n, err := p.f.Read(msg)
			if err != nil {
				if errors.Is(err, syscall.EPIPE) {
					logger.Warnf("short read from kmsg; skipping")
					continue
				}
				if err == io.EOF {
					logger.Infof("kmsg reader closed, shutting down")
					return
				}
				logger.Errorf("error reading /dev/kmsg: %v", err)
				return
			}

			msgStr := string(msg[:n])
			for _, exception := range kmsgExceptions {
				match, err := regexp.MatchString(exception.Message, msgStr)
				if err != nil || !match {
					continue
				}
				p.exception <- KmsgException{
					Name:    exception.Name,
					Message: msgStr,
				}
				break
			}
		}
	}()
	return p.exception
}

func (p *parser) Close() error {
	return p.f.Close()
}
