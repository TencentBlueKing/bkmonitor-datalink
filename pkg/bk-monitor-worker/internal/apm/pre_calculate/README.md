# APM-Pre-Calculate

## 目录结构

core: 公共代码层

notifier: 消息队列实现类
    有以下组件:
        1. Kafka: 对接Kafka队列

window: 窗口处理逻辑
    有以下组件:
        1. Runtime: 决定窗口过期时间如何处理、或者记录额外数据
        2. Handler: 不同的窗口处理逻辑。目前有distributiveWindow父子窗口

storage: 存储实现类
    有以下组件:
        1. ES
        2. Redis

config.go: 配置参数

## 单机启动

不启动 bmw 其他模块的前提下，APM 预计算支持独立启动。并且可以做到不依赖外部第三方组件。

启动命令：
```bash
./bmw start_from_file [-f <connection_file>]
```

connection_file 为任务定义的文件路径，文件内容为指定需要运行的预计算任务，命令启动后即会自动运行，并且能够监听更改，文件内容格式参照 internal/apm/pre_calculate/connections_test.yaml

