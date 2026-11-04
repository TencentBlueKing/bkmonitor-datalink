// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package configs

import (
	"errors"
	"fmt"
	"strings"
)

const (
	beatOutputConfigField = "output"
	beatGSEConfigField    = "bkpipe"
)

// beatGSEConfig 只取本模块需要的 output.bkpipe.endpoint，字段名与主配置一致。
type beatGSEConfig struct {
	Endpoint string `config:"endpoint"`
}

// ActiveProfileGSEEndpoint 返回本机 GSE Agent 的数据上报地址，取自主配置的
// output.bkpipe.endpoint。常驻 Beat 的 Event 与产物走 Beat 自己的 gse output，
// 不经过这里；只有轻量 CLI 需要它——CLI 运行在 Beat 框架之外，必须自建 GSE 客户端
// 才能上报 finished 与 pprof。
//
// 不使用 libgse 的内置默认路径：Agent 的安装目录随部署方式变化，猜错会连到不存在
// 的 socket，而主配置里的值就是本机插件正在用的那个。
func ActiveProfileGSEEndpoint() (string, error) {
	rawConfig, err := loadBeatRawConfig()
	if err != nil {
		return "", err
	}
	if !rawConfig.HasField(beatOutputConfigField) {
		return "", errors.New("main configuration has no output section: output.bkpipe.endpoint is required to report through the local GSE agent")
	}
	outputChild, err := rawConfig.Child(beatOutputConfigField, -1)
	if err != nil {
		return "", fmt.Errorf("read output section: %w", err)
	}
	if !outputChild.HasField(beatGSEConfigField) {
		return "", errors.New("main configuration has no output.bkpipe section: reporting through the local GSE agent is not configured")
	}
	gseChild, err := outputChild.Child(beatGSEConfigField, -1)
	if err != nil {
		return "", fmt.Errorf("read output.bkpipe section: %w", err)
	}
	var gseConfig beatGSEConfig
	if err := gseChild.Unpack(&gseConfig); err != nil {
		return "", fmt.Errorf("unpack output.bkpipe section: %w", err)
	}
	gseConfig.Endpoint = strings.TrimSpace(gseConfig.Endpoint)
	if gseConfig.Endpoint == "" {
		return "", errors.New("output.bkpipe.endpoint is required to report through the local GSE agent")
	}
	return gseConfig.Endpoint, nil
}
