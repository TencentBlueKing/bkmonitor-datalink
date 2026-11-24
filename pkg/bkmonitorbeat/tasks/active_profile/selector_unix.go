// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !windows

package active_profile

import "syscall"

// openNonBlock 让打开 FIFO 时立即返回，而不是阻塞到出现写端。
const openNonBlock = syscall.O_NONBLOCK
