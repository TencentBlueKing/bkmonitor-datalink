// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package configs

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/elastic/beats/libbeat/cfgfile"
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

// ActiveProfileDirName 是 Active Profiling 在 Beat 数据目录下的固定子目录。
const ActiveProfileDirName = "active_profile"

const beatPathConfigField = "path"

// beatPathConfig 只取本模块需要的 path.data，字段名与主配置 path 段一致。
type beatPathConfig struct {
	DataPath string `config:"data"`
}

var (
	activeProfileOutputRootOnce sync.Once
	activeProfileOutputRoot     string
	activeProfileOutputRootErr  error
)

// ActiveProfileOutputRoot 返回 <path.data>/active_profile。常驻 Beat 与轻量 CLI 都
// 从主配置的 path.data 推导，两个进程因此得到同一个绝对路径；取消命令正是靠它按
// 执行键定位运行目录。该路径不属于下发协议，平台不下发、用户也不能改。
func ActiveProfileOutputRoot() (string, error) {
	activeProfileOutputRootOnce.Do(func() {
		activeProfileOutputRoot, activeProfileOutputRootErr = resolveActiveProfileOutputRoot()
	})
	return activeProfileOutputRoot, activeProfileOutputRootErr
}

func resolveActiveProfileOutputRoot() (string, error) {
	rawConfig, err := loadBeatRawConfig()
	if err != nil {
		return "", err
	}
	if !rawConfig.HasField(beatPathConfigField) {
		return "", errors.New(
			"main configuration has no path section: path.data is required to locate active profiling run directories",
		)
	}
	pathChild, err := rawConfig.Child(beatPathConfigField, -1)
	if err != nil {
		return "", fmt.Errorf("read path section: %w", err)
	}
	var pathConfig beatPathConfig
	if err := pathChild.Unpack(&pathConfig); err != nil {
		return "", fmt.Errorf("unpack path section: %w", err)
	}
	// 必须是绝对路径：常驻 Beat 与 JOB 拉起的轻量 CLI 工作目录不同，相对 path.data
	// 会让两个进程算出不同的运行目录，取消命令就永远定位不到采集进程。
	if !filepath.IsAbs(pathConfig.DataPath) {
		return "", fmt.Errorf(
			"path.data %q must be absolute so the collector and the cancel command resolve the same run directory",
			pathConfig.DataPath,
		)
	}
	return filepath.Join(pathConfig.DataPath, ActiveProfileDirName), nil
}

// loadBeatRawConfig 优先使用常驻 Beat 已经加载的主配置；轻量 CLI 运行在 Beat 框架
// 初始化之前，自行读取一次主配置。cfgfile.Load 只做 YAML 读取与合并，不创建 Beat
// 实例、不连接 GSE、不抢 PID 锁、也不打开本地存储。
func loadBeatRawConfig() (*common.Config, error) {
	if loaded := beat.GetRawConfig(); loaded != nil {
		return loaded, nil
	}
	rawConfig, err := cfgfile.Load("", nil)
	if err != nil {
		return nil, fmt.Errorf("load main configuration (the lightweight CLI must pass -c): %w", err)
	}
	return rawConfig, nil
}
