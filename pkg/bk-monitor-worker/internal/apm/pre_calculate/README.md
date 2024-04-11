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
