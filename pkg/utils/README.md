# utils

> 蓝鲸监控 Golang 工具包

## 模块介绍

### host

监控主机标识。

### logger

日志库，封装了 go.uber.org/zap 和 lumberjack.v2 支持日志切割。

### notifier

监控文件系统中特定模式的文件变化并发出通知。

### pprofsnapshot

收集 Go 程序的性能分析数据 pprof profiles，并将其压缩。

### register

consul 服务注册与健康检查。

### router

用于管理 InfluxDB 的相关信息，包括集群信息、主机信息、标签信息等。

### time

解析一个表示时间持续的字符串，并将其转换为 Go 语言中的 time.Duration 类型。

### validator

监控数据上报校验。

## Contributing

我们诚挚地邀请你参与共建蓝鲸开源社区，通过提 bug、提特性需求以及贡献代码等方式，一起让蓝鲸开源社区变得更好。

![bkmonitor-kits](https://user-images.githubusercontent.com/19553554/126454082-d21b22f9-6df9-487f-82c1-a9dcd054f29a.png)
