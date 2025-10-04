# WuKongIM API 命令清单

本文档整理了学习过程中常用的 WuKongIM API 命令，方便快速查阅和测试。

---

## 📊 一、服务状态检查

### 1.1 检查进程状态
```bash
ps aux | grep -E "wukongim" | grep -v grep
```
**作用**：查看 WuKongIM 进程是否在运行，显示进程 ID、CPU、内存占用等信息

---

### 1.2 查看监听端口
```bash
# 方法1：使用 lsof（推荐）
lsof -iTCP -sTCP:LISTEN -n -P | grep wukongim

# 方法2：使用 netstat
netstat -an | grep LISTEN | grep -E "5001|5100|5200|5172|5300"

# 方法3：使用 ss
ss -tln | grep -E "5001|5100|5200|5172|5300"
```
**作用**：查看 WuKongIM 监听的所有端口，验证服务是否正常启动

**关键端口说明**：
- `5100`：TCP 客户端长连接（二进制协议）
- `5200`：WebSocket 连接（二进制/JSON 协议）
- `5001`：HTTP API 接口
- `5300`：后台管理系统
- `5172`：聊天 Demo
- `11110`：集群内部通信

---

### 1.3 获取服务器信息
```bash
curl -s http://127.0.0.1:5001/varz
```
**作用**：获取服务器详细运行状态，包括：
- 服务器 ID、版本信息
- 当前连接数
- 运行时长（uptime）
- 内存、CPU 使用情况
- 消息收发统计
- 监听地址

**返回示例**：
```json
{
  "server_id": "1001",
  "server_name": "WuKongIM",
  "connections": 0,
  "uptime": "1h54m47s",
  "mem": 131960832,
  "cpu": 1.8,
  "in_msgs": 133,
  "out_msgs": 131,
  "tcp_addr": "192.168.10.114:5100",
  "ws_addr": "ws://192.168.10.114:5200"
}
```

---

### 1.4 健康检查（简单）
```bash
curl -s http://127.0.0.1:5001/varz 
```
**作用**：快速检查 API 服务是否可用

---

## 📨 二、消息发送

### 2.1 发送单聊消息
```bash
curl -X POST "http://127.0.0.1:5001/message/send" \
  -H "Content-Type: application/json" \
  -d '{
    "header": {
      "no_persist": 0
    },
    "from_uid": "user001",
    "channel_id": "user002",
    "channel_type": 1,
    "payload": "SGVsbG8gV3VLb25nSU0h"
  }'
```

**参数说明**：
- `from_uid`：发送者 UID
- `channel_id`：接收者频道 ID（单聊时为对方 UID）
- `channel_type`：频道类型
  - `1` = Person（单聊）
  - `2` = Group（群聊）
  - `3` = Community（社区）
  - `4` = CustomerService（客服）
  - `5` = Info（系统通知）
- `payload`：消息内容（**Base64 编码**）
- `header.no_persist`：是否持久化（0=持久化，1=不持久化）

**payload 编码示例**：
```bash
# "Hello WuKongIM!" 的 Base64 编码
echo -n "Hello WuKongIM!" | base64
# 输出：SGVsbG8gV3VLb25nSU0h
```

**返回示例**：
```json
{
  "status": 200,
  "data": {
    "client_msg_no": "373bfa18f66e472dad82d0584c14ab120",
    "message_id": 1973100073256390656
  }
}
```

---

### 2.2 发送群聊消息
```bash
curl -X POST "http://127.0.0.1:5001/message/send" \
  -H "Content-Type: application/json" \
  -d '{
    "from_uid": "user001",
    "channel_id": "group001",
    "channel_type": 2,
    "payload": "SGVsbG8gR3JvdXAh"
  }'
```
**作用**：向群聊 `group001` 发送消息

---

### 2.3 发送 JSON 格式消息
```bash
# 1. 先构造 JSON 消息
MESSAGE_JSON='{"content":"你好，WuKongIM！","type":1}'

# 2. Base64 编码
PAYLOAD=$(echo -n "$MESSAGE_JSON" | base64)

# 3. 发送
curl -X POST "http://127.0.0.1:5001/message/send" \
  -H "Content-Type: application/json" \
  -d "{
    \"from_uid\": \"user001\",
    \"channel_id\": \"user002\",
    \"channel_type\": 1,
    \"payload\": \"$PAYLOAD\"
  }"
```
**作用**：发送结构化的 JSON 消息内容

---

## 📬 三、消息查询

### 3.1 同步频道消息
```bash
curl -s "http://127.0.0.1:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid": "user002",
    "channel_id": "user001",
    "channel_type": 1,
    "start_message_seq": 0,
    "pull_mode": 1,
    "limit": 10
  }' | python3 -m json.tool
```

**参数说明**：
- `login_uid`：当前登录用户的 UID
- `channel_id`：要查询的频道 ID
- `channel_type`：频道类型（1=单聊，2=群聊）
- `start_message_seq`：起始消息序号（0 表示从最新开始）
- `pull_mode`：拉取模式
  - `1` = 向下拉取（Pull Down，获取更新的消息）
  - `0` = 向上拉取（Pull Up，获取更旧的消息）
- `limit`：返回消息数量限制

**返回示例**：
```json
{
  "start_message_seq": 0,
  "end_message_seq": 0,
  "more": 0,
  "messages": [
    {
      "message_id": 1973100073256390656,
      "message_seq": 1,
      "from_uid": "user001",
      "channel_id": "user002",
      "channel_type": 1,
      "timestamp": 1759258690,
      "payload": "SGVsbG8gV3VLb25nSU0h"
    }
  ]
}
```

---

### 3.2 解码消息内容
```bash
# 解码 Base64 payload
echo "SGVsbG8gV3VLb25nSU0h" | base64 -d
# 输出：Hello WuKongIM!
```
**作用**：将 Base64 编码的消息内容解码为可读文本

---

### 3.3 查询指定序号范围的消息
```bash
curl -s "http://127.0.0.1:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid": "user001",
    "channel_id": "user002",
    "channel_type": 1,
    "start_message_seq": 10,
    "pull_mode": 0,
    "limit": 20
  }'
```
**作用**：从消息序号 10 开始，向上拉取 20 条历史消息

---

## 👥 四、频道管理

### 4.1 创建频道
```bash
curl -X POST "http://127.0.0.1:5001/channel" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "group001",
    "channel_type": 2
  }'
```
**作用**：创建一个群聊频道

---

### 4.2 获取频道信息
```bash
curl -s "http://127.0.0.1:5001/channel/info?channel_id=user002&channel_type=1"
```
**作用**：查询指定频道的详细信息

---

### 4.3 添加订阅者
```bash
curl -X POST "http://127.0.0.1:5001/channel/subscriber_add" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "group001",
    "channel_type": 2,
    "subscribers": ["user001", "user002", "user003"]
  }'
```
**作用**：将多个用户添加到频道的订阅列表（加入群聊）

---

### 4.4 移除订阅者
```bash
curl -X POST "http://127.0.0.1:5001/channel/subscriber_remove" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "group001",
    "channel_type": 2,
    "subscribers": ["user003"]
  }'
```
**作用**：将用户从频道的订阅列表中移除（退出群聊）

---

## 🔍 五、用户与连接管理

### 5.1 查询在线用户
```bash
curl -s "http://127.0.0.1:5001/users/online"
```
**作用**：获取当前在线的所有用户列表

---

### 5.2 查询用户连接信息
```bash
curl -s "http://127.0.0.1:5001/user/connection?uid=user001"
```
**作用**：查询指定用户的连接详情（设备、节点等）

---

### 5.3 踢出用户连接
```bash
curl -X POST "http://127.0.0.1:5001/user/kick" \
  -H "Content-Type: application/json" \
  -d '{
    "uid": "user001"
  }'
```
**作用**：强制断开用户的所有连接

---

## 📊 六、统计与监控

### 6.1 获取系统统计
```bash
curl -s "http://127.0.0.1:5001/varz" | python3 -m json.tool
```
**作用**：格式化输出系统统计信息

---

### 6.2 获取频道统计
```bash
curl -s "http://127.0.0.1:5001/channel/stat?channel_id=group001&channel_type=2"
```
**作用**：获取指定频道的统计信息（消息数、订阅者数等）

---

## 🔧 七、高级功能

### 7.1 发送流式消息（Stream Message）
```bash
# 开始流式消息
curl -X POST "http://127.0.0.1:5001/stream/start" \
  -H "Content-Type: application/json" \
  -d '{
    "stream_no": "stream_001",
    "from_uid": "ai_bot",
    "channel_id": "user001",
    "channel_type": 1
  }'

# 发送流式消息分片
curl -X POST "http://127.0.0.1:5001/stream/chunk" \
  -H "Content-Type: application/json" \
  -d '{
    "stream_no": "stream_001",
    "payload": "base64_encoded_chunk"
  }'

# 结束流式消息
curl -X POST "http://127.0.0.1:5001/stream/end" \
  -H "Content-Type: application/json" \
  -d '{
    "stream_no": "stream_001"
  }'
```
**作用**：发送流式消息（适用于 AI 对话等场景）

---

### 7.2 发送指令消息（不持久化）
```bash
curl -X POST "http://127.0.0.1:5001/message/send" \
  -H "Content-Type: application/json" \
  -d '{
    "header": {
      "no_persist": 1
    },
    "from_uid": "system",
    "channel_id": "user001",
    "channel_type": 1,
    "payload": "base64_encoded_command"
  }'
```
**作用**：发送不会被存储的指令消息（适用于实时信令）

---

## 🎯 八、实用脚本

### 8.1 批量发送测试消息
```bash
#!/bin/bash
# 批量发送 100 条测试消息

for i in {1..100}; do
  MESSAGE="Test message $i"
  PAYLOAD=$(echo -n "$MESSAGE" | base64)

  curl -X POST "http://127.0.0.1:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"from_uid\": \"user001\",
      \"channel_id\": \"user002\",
      \"channel_type\": 1,
      \"payload\": \"$PAYLOAD\"
    }"

  echo "Sent message $i"
  sleep 0.1
done
```

---

### 8.2 消息性能测试
```bash
#!/bin/bash
# 测试消息发送性能

START=$(date +%s)
COUNT=1000

for i in $(seq 1 $COUNT); do
  curl -X POST "http://127.0.0.1:5001/message/send" \
    -H "Content-Type: application/json" \
    -d '{"from_uid":"user001","channel_id":"user002","channel_type":1,"payload":"dGVzdA=="}' \
    > /dev/null 2>&1
done

END=$(date +%s)
DURATION=$((END - START))
QPS=$((COUNT / DURATION))

echo "发送 $COUNT 条消息"
echo "耗时: $DURATION 秒"
echo "QPS: $QPS 条/秒"
```

---

### 8.3 监控脚本
```bash
#!/bin/bash
# 实时监控 WuKongIM 服务状态

while true; do
  clear
  echo "=== WuKongIM 服务监控 ==="
  echo ""

  # 服务状态
  curl -s http://127.0.0.1:5001/varz | python3 -m json.tool | grep -E "connections|uptime|mem|cpu|in_msgs|out_msgs"

  echo ""
  echo "按 Ctrl+C 退出"
  sleep 2
done
```

---

## 📝 九、常用工具命令

### 9.1 格式化 JSON 输出
```bash
# 使用 python3
curl -s http://127.0.0.1:5001/varz | python3 -m json.tool

# 使用 jq（需要安装）
curl -s http://127.0.0.1:5001/varz | jq '.'
```

---

### 9.2 Base64 编码/解码
```bash
# 编码
echo -n "Hello WuKongIM!" | base64

# 解码
echo "SGVsbG8gV3VLb25nSU0h" | base64 -d
```

---

### 9.3 抓包分析
```bash
# 抓取 5001 端口的 HTTP 流量
sudo tcpdump -i any -A 'port 5001' -w wukongim.pcap

# 抓取 5100 端口的 TCP 流量
sudo tcpdump -i any -X 'port 5100' -w wukongim_tcp.pcap
```

---

## 🔗 十、相关链接

- **官方文档**：https://githubim.com
- **API 文档**：https://githubim.com/api
- **管理后台**：http://127.0.0.1:5300/web
- **聊天 Demo**：http://127.0.0.1:5172/chatdemo/

---

## 💡 常见问题

### Q1: 为什么 curl 返回 404？
**A**: 检查接口路径是否正确，某些接口可能在不同版本中有变化。使用 `/varz` 作为健康检查更可靠。

---

### Q2: payload 必须 Base64 编码吗？
**A**: 是的，WuKongIM 的消息内容必须使用 Base64 编码传输，这样可以支持二进制数据。

---

### Q3: 单聊为什么要查询对方的频道？
**A**: 在 WuKongIM 中，单聊是双向的：
- `user001` 给 `user002` 发消息 → 消息存储在频道 `user002`
- `user002` 查询时应该查 `channel_id=user001` 才能看到对方发来的消息

---

### Q4: 如何实现已读回执？
**A**: 需要结合业务逻辑，通常通过发送指令消息（no_persist=1）告知对方已读状态。

---

## 📅 更新日志

- **2025-10-01**：初始版本，整理基础 API 命令
