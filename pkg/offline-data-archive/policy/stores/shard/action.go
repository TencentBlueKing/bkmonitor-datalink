// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shard

import (
	"context"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
	oleltrace "go.opentelemetry.io/otel/trace"
	"os"
	"path/filepath"

	"github.com/influxdata/influxdb/cmd/influx_inspect/buildtsi"
	"github.com/influxdata/influxdb/logger"
	"github.com/influxdata/influxdb/tsdb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/instance"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

type Action interface {
	Move(ctx context.Context, shard *Shard) error
	Clean(ctx context.Context, shard *Shard) error
	Rebuild(ctx context.Context, shard *Shard) error
}

var _ Action = (*BaseAction)(nil)

type BaseAction struct {
	log log.Logger
}

func (a *BaseAction) Move(ctx context.Context, s *Shard) error {
	// 获取操作实例
	ins, err := instance.GetInstance(s.Spec.Target.InstanceType)
	if err != nil {
		return err
	}

	// 判断文件是否已经存在
	ok, err := ins.Exist(ctx, s.Spec.Target.Path)
	if err != nil {
		return err
	}

	// 如果目标不存在则进行一下操作
	if !ok {
		a.log.Infof(ctx, "%s is not exist need move", s.Spec.Target.Path)
		// 判断来源文件是否存在，不存在则报错
		_, err = os.Stat(s.Spec.Source.Path)
		if err != nil {
			return err
		}

		// 上传操作
		err = ins.Upload(ctx, s.Spec.Source.Path, s.Spec.Target.Path)
		if err != nil {
			return err
		}
	}

	s.Status.Code = Rebuild
	return err
}

func (a *BaseAction) Clean(ctx context.Context, s *Shard) error {
	// 获取操作实例
	ins, err := instance.GetInstance(s.Spec.Target.InstanceType)
	if err != nil {
		return err
	}

	// 如果文件存在则删除
	ok, err := ins.Exist(ctx, s.Spec.Target.Path)
	if err != nil {
		return err
	}

	if ok {
		err = ins.Delete(ctx, s.Spec.Target.Path)
	}
	return err
}

func (a *BaseAction) Rebuild(ctx context.Context, s *Shard) error {
	var (
		span oleltrace.Span
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "rebuild")
	if span != nil {
		defer span.End()
	}

	// 获取操作实例
	ins, err := instance.GetInstance(s.Spec.Target.InstanceType)
	if err != nil {
		return err
	}

	trace.InsertStringIntoSpan("shard-json", fmt.Sprintf("%+v", s), span)

	trace.InsertStringIntoSpan("shard-unique", s.Unique(), span)
	trace.InsertStringIntoSpan("final-json", fmt.Sprintf("%+v", s.Spec.Final), span)

	// 判断文件是否已经存在
	ok, err := ins.Exist(ctx, s.Spec.Final.Path)
	if err != nil {
		return err
	}

	// 如果目标不存在则进行一下操作
	if !ok {
		a.log.Infof(ctx, "%s need rebuild", s.Spec.Target.Path)
		// 判断来源文件是否存在，不存在则报错
		ok, err := ins.Exist(ctx, s.Spec.Target.Path)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s is not exist for rebuild", s.Spec.Target.Path)
		}

		// 下载到临时文件夹进行处理
		tempPath, err := ins.Download(ctx, s.Spec.Target.Path, "dist/cos_temp")
		if err != nil {
			return err
		}
		defer func() {
			// 用完清理 tmpPath 目录
			err = os.RemoveAll(tempPath)
			if err != nil {
				s.Log.Errorf(ctx, "remove target path, err: %s", err)
				return
			}
		}()

		walPath := filepath.Join(tempPath, "wal")
		_, err = os.Stat(walPath)
		if err != nil {
			err = os.Mkdir(walPath, 0755)
			if err != nil {
				a.log.Errorf(ctx, "create wal folder error, err:%s", err)
				return err
			}
		}

		// 删除目标路径下的index文件
		a.log.Infof(ctx, "start rebuild shard, shard key :%s", s.Unique())
		indexFile := filepath.Join(tempPath, "index")
		_, err = os.Stat(indexFile)
		if err == nil {
			err = os.RemoveAll(indexFile)
			if err != nil {
				s.Log.Errorf(ctx, "remove index folder error")
				return err
			}
		}

		seriesPath := filepath.Join(tempPath, "_series")
		sFile := tsdb.NewSeriesFile(seriesPath)
		engineOption := tsdb.NewEngineOptions()
		engineOption.IndexVersion = tsdb.TSI1IndexName
		engineOption.ShardID = 0
		// 尝试先重建tsi索引
		err = sFile.Open()
		if err != nil {
			s.Log.Errorf(ctx, "open series file error, err: %s", err)
			return err
		}
		defer sFile.Close()

		trace.InsertStringIntoSpan("s-file-path", sFile.Path(), span)
		trace.InsertStringIntoSpan("temp-path", tempPath, span)
		trace.InsertStringIntoSpan("wal-path", walPath, span)

		err = buildtsi.IndexShard(
			sFile,
			tempPath,
			walPath,
			tsdb.DefaultMaxIndexLogFileSize,
			tsdb.DefaultCacheMaxMemorySize,
			10000,
			logger.New(os.Stderr),
			false,
		)
		if err != nil {
			s.Log.Errorf(ctx, "rebuild tsi error, err: %s", err)
			return err
		}

		err = ins.Upload(ctx, tempPath, s.Spec.Final.Path)
		if err != nil {
			s.Log.Errorf(ctx, "upload error target: %s, new path: %s, err: %s", tempPath, s.Spec.Final.Path, err)
			return err
		}
	}

	s.Status.Code = Finish
	return err
}

func GetAction(action Action, s *Shard) func(ctx context.Context, shard *Shard) error {
	// 操作 shard
	switch s.Status.Code {
	case Move:
		return action.Move
	case Rebuild:
		return action.Rebuild
	default:
		return nil
	}
}
