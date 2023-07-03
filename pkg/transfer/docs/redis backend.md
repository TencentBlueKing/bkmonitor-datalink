#### redis 配置
    --公共配置 参考influxdb

#### 客户端
    -- pipeline
    -- 写入方式为队列(list)
    -- 写入服务端为: 
    -- 验证: 参考kafka backend
    
#### 写入
    -- 重试:由go-redis options 参数实现
    -- 批次:
    -- 队列最大长度:
    -- 消息间隔: 100ms

#### 异常
    -- 超时异常由go-redis提供
    -- 队列长度满:则阻塞;每 x ms轮询一次    
    -- 批次:批次处理方式,参考influxd
    -- 提交:pipeline 每次exec 算作一次提交
    -- 队列长度:每次提交之前会查询一次队列长度
    
#### 性能报告
    -- 

#### 其它
    -- 由于和go-redis/redis 名字冲突,package name 暂定 redisBackend

#### TODO
    -- todo :轮询 x 次后 kill 该流水线

