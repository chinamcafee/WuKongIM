# v3 冷频道历史读取与重启恢复修复

本轮从原版 main `ded2c0bd4` 建立独立工作树 `codex/cold-history-recovery`，先复现再修复；随后将相同产品代码补丁应用到 RedDot 和迁移工作区。没有改动线上部署或源 v2 数据。

## 结论和范围

已关闭本机功能验收中的“三节点冷重启遗漏历史末条消息”和“空副本仅靠读请求不能恢复”阻断。HTTP 普通历史同步与会话头读取复用原生 quorum 恢复，不需要追加业务消息。迁移工具的整体 `cutover_ready` 仍不能据此改为 true。

## 根因与修复

1. 正常 v3 quorum 写入成功后，运行态 HW 已覆盖末条，但磁盘 HW 检查点可以滞后。冷历史读只查询已加载运行态；运行态不存在时便直接使用旧检查点，因此返回 N−1 条且无错误。
2. 空副本的本地 HW=LEO=0 不能证明频道历史为空。冷 quorum Leader 必须使用服务节点重新解析的权威元数据，调用现有安装/恢复流程（包括原有任期屏障），再探测已恢复 HW。激活按频道合并，使用现有 16 个工作者上限和请求取消边界；热频道仍使用一次批量运行态探测。
3. 已加载元数据不等于恢复完成。运行态增加 `RecoveryRequired` 观测，区分安装中、安装失败与已恢复；写入栅栏不会单独阻断已经恢复的读取。恢复或权威证据失败保留逐项错误，不返回残缺的成功页。
4. 同一路径的边界测试复现了原有倒序漏洞：HW=0 被转换为存储层“无限上界”的 MaxSeq=0。现在在调用存储前直接返回空页，防止暴露未提交尾部。

数据库主键、列、行编码、兼容消息载荷、v1 proposal 摘要、quorum 提交规则均未修改。此次不是 v2 数据特例，也没有清空 StreamNo、Setting、RedDot 或重编原始标识。

## 验证证据

| 场景 | 原版 main | 修复后 |
| --- | --- | --- |
| 原生三节点，3 条 quorum ACK，完整 Leader 重启，仅请求历史 | 25 秒内持续 2/3，主记录仍在 | 3/3，无额外发送 |
| 相同场景，重启 Leader 使用空消息目录 | 25 秒内持续 0/3 | 3/3，原标识、序号、正文、时间恢复 |
| HTTP 单节点集群发送后全部重启 | 通过 | 2/2 |
| HTTP 三节点从每个入口发送，再全部重启 | 先前精确原版对照 6→5 | 各入口 6/6 |
| RedDot=0/1，三节点转发及全部重启 | 旧 RedDot 缺陷另有报告 | 原标识、序号、正文和 RedDot 全部保持 |
| 兼容迁移样本导入后的单节点重启 | 已有测试 | 通过 |
| 兼容迁移样本导入后的三节点空副本恢复 | 先前失败 | 通过，仅请求历史 |
| 已加载但恢复未完成、恢复失败或缺失权威元数据 | 可被当作旧/空历史成功 | 返回错误，不能使用旧 HW |
| 倒序读取 HW=0、LEO=2 | 错误暴露两条未提交记录 | 空页；HW=1 只返回一条 |

HTTP 回归同时断言发送 ID 唯一、返回 ID 无重复、总数及逐条字段相等，避免“数量相等但有重复/遗漏”的假通过。

验证命令：

```sh
# 原始复现文件不变；红灯与绿灯分别留证
GOWORK=off go test -tags=integration ./tmp/recovery-control -run TestNativeV3 -count=1 -v -timeout=3m
# 正式产品回归
GOWORK=off go test -tags=integration ./pkg/cluster ./internal/app -run TestColdHistory -count=1 -timeout=3m
GOWORK=off go test ./pkg/channel/... ./pkg/cluster/... ./internal/app -count=1
GOWORK=off go test -race ./pkg/channel/reactor ./pkg/cluster/channels -run 'Test(RuntimeProbe.*(Recovery|Install)|CommittedRead|ServiceReadConversationHeads.*Cold)' -count=1
# RedDot 工作区及迁移工作区
GOWORK=off go test -tags=integration ./internal/app -run TestRedDotHTTPCluster -count=1 -timeout=3m
# 迁移工作区
GOWORK=off go test -tags=integration ./internal/infra/migrationv2 -run TestCompatibleMigration -count=1 -timeout=4m
GOWORK=off go test ./internal/usecase/migration ./internal/infra/migrationv3 ./pkg/cluster/channels ./pkg/channel/reactor -count=1
```

三个工作区的 `flow-doc-contracts` 命名检查均通过；原版 main 中两个未修改 FLOW 的行数警告保留。原版产品与迁移相关单元测试、上述集成测试及聚焦 race 检查通过。Darwin race 链接器有现存 LC_DYSYMTAB 警告，无数据竞争报告。

私有证据保存在独立工作树 `tmp/cold-history-evidence/`：原版失败日志、未改变的原始复现、修复后日志、组合验证日志、规则上下文摘要及补丁校验和。最初候选中遗漏本机读取元数据的失败日志也保留，最终证据以 `original-loop-fixed.log`、`final-*.log` 和 `migration-recovery.log` 为准。

## 仍未完成的迁移验收

- 15 条保留流主消息的 StreamNo/流语义兼容。只排除旧流分片和元数据；不额外排除主消息，不清空字段。
- 暂缓的插件兼容及其余完整 prepare 准入项。
- 100 GiB／4 小时性能验收、SDK 缓存与游标切换，以及实际目标集群的全量现场验收。

当前仅验证本机合成数据。冷读现在可能需要 quorum 网络恢复，其延迟、资源占用及不可用时返回错误属于明确行为；没有据此作出性能承诺，也未执行生产导入或切流。
