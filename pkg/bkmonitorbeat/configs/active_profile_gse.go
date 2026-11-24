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

// beatGSEConfig 只取本模块需要的 output.bkpipe 字段，字段名与 libgse output.gse 一致。
type beatGSEConfig struct {
	Endpoint     string `config:"endpoint"`
	BKAddressing string `config:"bk_addressing"`
	HostIP       string `config:"hostip"`
	CloudID      *int32 `config:"cloudid"`
	HostID       int32  `config:"hostid"`
}

// ActiveProfileGSEConfig 是从主配置 output.bkpipe 读取的本机 GSE Agent 连接与主机身份覆写。
type ActiveProfileGSEConfig struct {
	Endpoint     string
	BKAddressing string
	HostIP       string
	CloudID      *int32
	HostID       int32
}

// ActiveProfileGSEConfigFromMain 读取主配置中的 output.bkpipe，供轻量 CLI 直连本机
// GSE Agent 并与常驻 Beat 的 AgentInfoFetcher 保持一致的主机身份覆写。
//
// endpoint 必须取自主配置，不使用 libgse 的内置默认路径：Agent 的安装目录随部署方式
// 变化，猜错会连到不存在的 socket，而主配置里的值就是本机插件正在用的那个。
func ActiveProfileGSEConfigFromMain() (ActiveProfileGSEConfig, error) {
	rawConfig, err := loadBeatRawConfig()
	if err != nil {
		return ActiveProfileGSEConfig{}, err
	}
	if !rawConfig.HasField(beatOutputConfigField) {
		return ActiveProfileGSEConfig{}, errors.New("main configuration has no output section: output.bkpipe.endpoint is required to report through the local GSE agent")
	}
	outputChild, err := rawConfig.Child(beatOutputConfigField, -1)
	if err != nil {
		return ActiveProfileGSEConfig{}, fmt.Errorf("read output section: %w", err)
	}
	if !outputChild.HasField(beatGSEConfigField) {
		return ActiveProfileGSEConfig{}, errors.New("main configuration has no output.bkpipe section: reporting through the local GSE agent is not configured")
	}
	gseChild, err := outputChild.Child(beatGSEConfigField, -1)
	if err != nil {
		return ActiveProfileGSEConfig{}, fmt.Errorf("read output.bkpipe section: %w", err)
	}
	var gseConfig beatGSEConfig
	if err := gseChild.Unpack(&gseConfig); err != nil {
		return ActiveProfileGSEConfig{}, fmt.Errorf("unpack output.bkpipe section: %w", err)
	}
	gseConfig.Endpoint = strings.TrimSpace(gseConfig.Endpoint)
	if gseConfig.Endpoint == "" {
		return ActiveProfileGSEConfig{}, errors.New("output.bkpipe.endpoint is required to report through the local GSE agent")
	}
	hostIP := strings.TrimSpace(gseConfig.HostIP)
	if parts := strings.Split(hostIP, ","); len(parts) >= 2 {
		hostIP = strings.TrimSpace(parts[0])
	}
	return ActiveProfileGSEConfig{
		Endpoint:     gseConfig.Endpoint,
		BKAddressing: strings.TrimSpace(gseConfig.BKAddressing),
		HostIP:       hostIP,
		CloudID:      gseConfig.CloudID,
		HostID:       gseConfig.HostID,
	}, nil
}
