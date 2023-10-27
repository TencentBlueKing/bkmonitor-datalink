# libgse

> libgse 是蓝鲸监控的底层通信组件

## 用途

如采集器等蓝鲸监控底层模块想要将数据上报给 gse agent 需要使用 libgse 提供的接口来实现。

## 组织目录

* beat: 配置相关。
* common: 公共接口。
* debug: 调试相关。
* docs: 文档相关。
* gse: 核心通信数据收发。
* logp: 日志相关。
* monitoring: Prometheus 指标上报。
* output: 核心通信 gse client 建立。
* pidfile: 获取进程相关信息。
* processor: 进程相关。
* reloader: 重启服务。
* storage: 存储相关。

## Contributing

我们诚挚地邀请你参与共建蓝鲸开源社区，通过提 bug、提特性需求以及贡献代码等方式，一起让蓝鲸开源社区变得更好。

![bkmonitorbeat](https://user-images.githubusercontent.com/19553554/126453851-c8d5f6f5-13b4-4f18-9789-f90062426d22.png)

## License

基于 MIT 协议，详细请参考 [LICENSE](./LICENSE)
