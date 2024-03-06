diffRedis:
  srcKey: ""
  bypassKey: ""
  keyType: "hash"
  srcRedis:
    mode: standalone
    db: 5
    dialTimeout: 10s
    readTimeout: 10s
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""
  bypassRedis:
    mode: standalone
    db: 5
    dialTimeout: 10s
    readTimeout: 10s
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""
srcConsul:
  svrName: ""
  address: "127.0.0.1:8500"
  path: "test_prefix/"
bypassConsul:
  svrName: ""
  address: "127.0.0.1:8500"
  path: "test_prefix_bypass/"