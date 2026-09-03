# standard 测试数据生成器

`rawgen` 为 all-in-one E2E、压测和问题复现生成 `standard` payload JSONL，并输出可计算的 Event、EventProcessing、Alert、AlertLog、Redis signal 和 Kafka Alert V1 预期结果。

- 相同 seed 和配置产生完全相同的数据；
- 生命周期块之间随机排列，块内保持稳定顺序；
- 覆盖 `active`、`recovered`、`closed`、`severity_rotation` 和 `cross_tenant`；
- `severity_rotation` 同时产生 warning 到 critical 的升级和随后 warning 的低等级抑制；
- 可指定重复 delivery、确定性坏消息、租户数和同等级 triggered 次数；
- 生成器只构造数据，不控制 Kafka 发送速率或采集吞吐指标。

按总量平均分配类型：

```bash
go run ./tests/tools/rawgen \
  -seed 20260831 \
  -count 10000 \
  -types active,recovered,closed,severity_rotation \
  -tenant-count 50 \
  -out /tmp/events.jsonl \
  -expected-out /tmp/expected.json
```

按类型精确分配：

```bash
go run ./tests/tools/rawgen \
  -mix active=5000,recovered=2000,closed=2000,severity_rotation=1000,cross_tenant=100 \
  -duplicates 500 \
  -invalid 100 \
  -min-updates 0 \
  -max-updates 5 \
  -out /tmp/events.jsonl
```

`-min-updates/-max-updates` 表示终态前额外同等级 `triggered` 的数量，不会生成已废弃的 `updated` action。也可使用 `-profile tests/e2e/allinone/testdata/profile.json`；`-profile` 与生成参数互斥，但可同时指定 `-out` 和 `-expected-out`。
