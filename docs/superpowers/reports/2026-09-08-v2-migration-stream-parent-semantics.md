# 15 条旧流主消息的语义核对

后续决定：用户明确“流消息暂时不迁移过来，但是最终要保持频道的 message_seq 严格递增”。因此下述保留父消息并清除 StreamNo 的建议不实施；改为显式排除完整流消息，见 [实施报告](2026-09-08-v2-migration-stream-exclusion.md)。下文保留核对时的事实和当时未执行的建议。

2026-09-08。使用既有原始捕获与消息诊断工作区进行有界查找，未重新打开原 v2 数据目录、未修改目标数据库。新探针对每条主消息重新验证原字段 SHA-256，再核对全部三个物理副本。此结论只证明这 15 条记录一致，不代替整个源集群的权威副本选择。

## 核对结果

| 项目 | 每节点／逻辑消息数 |
| --- | --- |
| 保留的主消息 | 15；三个节点共 45 个物理记录，原字段一致 |
| 正文为空 | 10 |
| 正文非空 | 5：一条 108 字节，四条 39 字节 |
| Setting | 全部为 2，保留流标志 |
| ClientMsgNo | 全部非空；15 条均与各自 StreamNo 不同 |
| 按现有频道 + ClientMsgNo 查找事件投影／事件游标 | 均为 0 |
| 按旧 StreamNo 查找旧元数据 | 15 |
| 对应旧流分片 | 297，仍按已确认范围排除 |

没有能够直接接入 v3 事件模型的现成投影，不能把 StreamNo 当作 ClientMsgNo 的别名，也不能凭空制造完成事件或结束状态。

## v3 现有行为

- 原版 v2 固定源码 `a888f895` 的 HTTP 消息响应已经不包含 StreamNo，流事件也按频道和 ClientMsgNo 关联。旧协议和旧插件仍有该字段，因此不能笼统称它为完全无用。
- v3 的消息兼容行保留了 StreamNo 列；正常 Channel Message／Record、复制恢复和产品历史响应不保留该值。现有存储往返实验验证：强行写入源行后，从原生日志读取并应用到副本，原 ID、序号、Setting、正文保留，StreamNo 变空。
- 当前 v3 的二进制协议第 5 版起不再携带旧 StreamNo／StreamId；协议第 2–4 版仍有相关编码。新的事件模型不等同于恢复旧流机制。
- 用户已排除旧流分片及元数据。这意味着 10 条原本正文为空的主消息仍应为空；工具不能使用已排除分片拼接正文，也不能把它们假定为正常完成的流消息。

源码依据：`internal/types/message.go`（原版 v2）、`internal/access/api/message_legacy_model.go`、`internal/usecase/message/sync.go`、`pkg/protocol/codec/send.go`、`pkg/protocol/codec/recv.go`、`internal/infra/migrationv3/native_message_fields_test.go`。

## 当时待确认的转换方案（已被后续决定取代）

| 字段／数据 | 建议处理 |
| --- | --- |
| 主消息 MessageID、ClientMsgNo、发送者、频道 | 保留原值 |
| MessageSeq | 沿用已批准的去重后序号映射 |
| Payload、Setting、RedDot | 原值写入 v3；空正文仍为空 |
| 主消息中的旧 StreamNo | 原值完整留在带校验和的源归档；v3 主消息不写该旧字段 |
| 旧 Stream／StreamMeta | 按既有排除策略归档，不导入 |
| v3 事件或终态 | 不制造、不回填 |

这个方案改变了此前“主消息原字段均保留”的范围，所以必须得到明确答复后才能加入可执行迁移策略。答复前，`stream_no` 阻断和目标转换拒绝保持有效。当前没有修改转换代码，也没有执行导入。

用户也可以要求 StreamNo 继续保留到目标。此时应保留阻断，另行评审 v3 对旧流协议/字段的正式兼容范围；不能为了通过迁移校验增加存储特例。

## 证据

私有目录：`tmp/server-rehearsal-20260908/stream-36/`。包含逐条脱敏关系、汇总、原生字段往返、旧表排除及协议编解码测试日志。探针位于 `tmp/migration-stream-semantics-probe/main.go`，只读取既有诊断缓存，不写源库或目标库。
