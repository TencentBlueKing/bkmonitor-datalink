####　测试指标
    -ACK 
    -分区方法
    -batchSize

#### 硬件配置
    宿主机
        -CPU型号:Intel® Core™ i5-8400 CPU
        -核数:6核
        -CPU频率:2.80GHz
        -内存容量:16GiB
        -OS:windows10
    虚拟机(kafka 所在机器)
        -CPU型号:Intel® Core™ i5-8400 CPU
        -核数:2核
        -CPU频率:2.80GHz
        -内存容量:7.1 GiB
        -OS:ubuntu 16.04 LTS

#### kafka 配置
    broker = 3

    Admin.Timeout = 3 * time.Second
    Net.MaxOpenRequests = 5
    Net.DialTimeout = 30 * time.Second
    Net.ReadTimeout = 30 * time.Second
    Net.WriteTimeout = 30 * time.Second
    Net.SASL.Handshake = true

    Metadata.Retry.Max = 3
    Metadata.Retry.Backoff = 250 * time.Millisecond
    Metadata.RefreshFrequency = 10 * time.Minute
    Metadata.Full = true
#### producer 配置
    Timeout = 10 * time.Second
    Partitioner = NewHashPartitioner
    Retry.Max = 3
    Retry.Backoff = 100 * time.Millisecond
    Return.Errors = true
    CompressionLevel = CompressionLevelDefault
    
    MaxRequestSize  = 100 * 1024 * 1024 该参数根据真实环境kafka(目前为中控kafka 设置) 可接收单条消息最大消息设置 (由于测试机性能原因,测试时该参数为1 *1024 * 1024)
    MaxMessage = 100 每100 条作为一个批次进行发送 若不考虑kafka 吞吐量 保证该批次大于1460 即可
    Frequency = 100ms 即是没填满100条,消息仍会每隔 100ms 发送一次;该参数参考influxdb flush设置
    
    注
    在当前配置下,够承受的单次最大数据量为(100 * 1000)条* (9 * 1024)byte , 约为 879m/次 
    或 (30 * 1000 )条 * (9 * 1024)byte/s 约为 264m/s
    当数据量超过或接近时,可以考虑在format 中加入限流节点
        

#### topic配置

    topic概要
        -testBenchmark : 3 partition 2 replication
        -testBenchmark3: 3 partition 3 replication
    topic description
        -Topic:testBenchmark    PartitionCount:3    ReplicationFactor:2 Configs:
            --Topic: testBenchmark    Partition: 0    Leader: 2   Replicas: 2,1   Isr: 2,1
            --Topic: testBenchmark    Partition: 1    Leader: 0   Replicas: 0,2   Isr: 0,2
            --Topic: testBenchmark    Partition: 2    Leader: 1   Replicas: 1,0   Isr: 1,0
        -Topic:testBenchmark2    PartitionCount:3    ReplicationFactor:3 Configs:
            --Topic: testBenchmark3   Partition: 0    Leader: 2   Replicas: 2,0,1 Isr: 2,0,1
            --Topic: testBenchmark3   Partition: 1    Leader: 0   Replicas: 0,1,2 Isr: 0,1,2
            --Topic: testBenchmark3   Partition: 2    Leader: 1   Replicas: 1,2,0 Isr: 1,2,0
            
    bin/kafka-topics.sh --create --zookeeper localhost:2181 --replication-factor 2 --partitions 3 --topic testBenchmark
    bin/kafka-topics.sh --create --zookeeper localhost:2181 --replication-factor 3 --partitions 3 --topic testBenchmark2


#### 测试方法
     - go test  backend_test.go -run="none"  -bench=. -benchtime="0.5s" -benchmem

#### 测试结果
    测试数据{"time":1558494970,"dimensions":{"tag":""},"metrics":{"field":11111}}
    testBenchmark:
    100000              5469 ns/op             854 B/op          8 allocs/op
    200000              4710 ns/op             825 B/op          8 allocs/op
    testBenchmark2:
    200000              4659 ns/op             823 B/op          8 allocs/op
    200000              4307 ns/op             740 B/op          7 allocs/op
    