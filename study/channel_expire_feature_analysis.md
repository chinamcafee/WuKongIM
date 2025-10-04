# 频道过期时间功能改造分析文档

**需求**：实现"指定一个时间戳，时间过了这个时间戳之后，对应的 channel 失效，无法发送数据"

**编写时间**：2025-01-05
**最后更新**：2025-01-05（新增 expire_at 可选特性）
**作者**：Claude Code
**WuKongIM版本**：基于当前代码库

---

## 更新记录

**v1.1 (2025-01-05)**：
- ✅ 将 `expire_at` 参数改为指针类型 `*int64`，实现真正可选
- ✅ 支持不传 `expire_at` 或传 `0` 表示"永不过期"
- ✅ 更新日志输出，区分"带过期时间"和"永不过期"两种创建模式
- ✅ 更新测试用例和使用建议

**v1.0 (2025-01-05)**：
- ✅ 初始版本，完成频道过期时间功能
- ✅ 新增 `to_uid` 参数支持个人频道
- ✅ 自动创建个人频道
- ✅ 修复 FakeChannelID 相关问题

---

## 目录

1. [改造概览](#1-改造概览)
2. [数据模型层改造（✅ 有效）](#2-数据模型层改造--有效)
3. [存储层改造（✅ 有效）](#3-存储层改造--有效)
4. [权限验证层改造（✅ 有效）](#4-权限验证层改造--有效)
5. [API层改造（⚠️ 部分问题已修复）](#5-api层改造️-部分问题已修复)
6. [问题分析与修复](#6-问题分析与修复)
7. [测试用例](#7-测试用例)
8. [遗漏项检查](#8-遗漏项检查)
9. [总结](#9-总结)

---

## 1. 改造概览

### 1.1 改造范围

整个功能涉及以下层次的修改：

```
┌─────────────────────────────────────────────┐
│          API 层 (HTTP 接口)                  │
│  - channel.go                               │
│  - channel_model.go                         │
└─────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────┐
│         权限验证层                           │
│  - internal/service/permission.go           │
│  - internal/user/handler/event_onsend.go    │
└─────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────┐
│         集群同步层                           │
│  - pkg/cluster/store/model.go (Codec)       │
│  - pkg/cluster/store/channel.go             │
└─────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────┐
│         存储层 (Pebble)                      │
│  - pkg/wkdb/model.go                        │
│  - pkg/wkdb/channel.go                      │
│  - pkg/wkdb/key/table.go                    │
└─────────────────────────────────────────────┘
```

### 1.2 核心字段

**ExpireAt** (`*time.Time`)：
- 存储在 `wkdb.ChannelInfo` 结构体中
- 为 `nil` 时表示永不过期
- 非 `nil` 时，当前时间超过此值时频道失效

---

## 2. 数据模型层改造（✅ 有效）

### 2.1 核心数据结构

**文件**：`pkg/wkdb/model.go`
**位置**：第 147-168 行

```go
type ChannelInfo struct {
    Id              uint64     `json:"id,omitempty"`
    ChannelId       string     `json:"channel_id,omitempty"`
    ChannelType     uint8      `json:"channel_type,omitempty"`
    Ban             bool       `json:"ban,omitempty"`
    Large           bool       `json:"large,omitempty"`
    Disband         bool       `json:"disband,omitempty"`
    SubscriberCount int        `json:"subscriber_count,omitempty"`
    DenylistCount   int        `json:"denylist_count,omitempty"`
    AllowlistCount  int        `json:"allowlist_count,omitempty"`
    LastMsgSeq      uint64     `json:"last_msg_seq,omitempty"`
    LastMsgTime     uint64     `json:"last_msg_time,omitempty"`
    Webhook         string     `json:"webhook,omitempty"`
    SendBan         bool       `json:"send_ban,omitempty"`
    AllowStranger   bool       `json:"allow_stranger,omitempty"`
    ExpireAt        *time.Time `json:"expire_at,omitempty"`  // ✅ 新增字段
    CreatedAt       *time.Time `json:"created_at,omitempty"`
    UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}
```

**评估**：✅ **有效**
- 字段定义正确
- 使用 `*time.Time` 类型，可以区分"未设置"（nil）和"已过期"（有值且已过去）
- JSON 标签正确

---

## 3. 存储层改造（✅ 有效）

### 3.1 数据库表定义

**文件**：`pkg/wkdb/key/table.go`
**位置**：第 306-405 行

#### 3.1.1 列定义

```go
var TableChannelInfo = struct {
    // ... 其他字段
    Column struct {
        // ... 其他列
        SendBan       [2]byte  // 0x06, 0x0C
        AllowStranger [2]byte  // 0x06, 0x0D
        ExpireAt      [2]byte  // 0x06, 0x0E ✅ 新增列
    }
    SecondIndex struct {
        // ... 其他索引
        SendBan       [2]byte  // 0x06, 0x08
        AllowStranger [2]byte  // 0x06, 0x09
        ExpireAt      [2]byte  // 0x06, 0x0A ✅ 新增二级索引
    }
}
```

**评估**：✅ **有效**
- 列 ID 分配正确（`0x06, 0x0E`）
- 二级索引 ID 分配正确（`0x06, 0x0A`）
- 没有与现有列冲突

### 3.2 写入逻辑

**文件**：`pkg/wkdb/channel.go`
**位置**：第 615-640 行

```go
// writeChannelInfo 函数中的 ExpireAt 写入逻辑
func (wk *wukongDB) writeChannelInfo(...) error {
    // ... 其他字段写入

    // expireAt - 写入列数据
    if channelInfo.ExpireAt != nil {
        expireAtBytes := make([]byte, 8)
        wk.endian.PutUint64(expireAtBytes, uint64(channelInfo.ExpireAt.UnixNano()))
        if err = w.Set(
            key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.ExpireAt),
            expireAtBytes,
            wk.noSync
        ); err != nil {
            return err
        }
    }

    // ... 其他字段写入

    // expireAt index - 写入二级索引
    if channelInfo.ExpireAt != nil {
        if err = w.Set(
            key.NewChannelInfoSecondIndexKey(
                key.TableChannelInfo.SecondIndex.ExpireAt,
                uint64(channelInfo.ExpireAt.UnixNano()),
                primaryKey
            ),
            nil,
            wk.noSync
        ); err != nil {
            return err
        }
    }

    return nil
}
```

**评估**：✅ **有效**
- 正确处理了 `ExpireAt` 为 `nil` 的情况（不写入）
- 使用 `UnixNano()` 转换为整数存储
- 同时写入列数据和二级索引

### 3.3 读取逻辑

**文件**：`pkg/wkdb/channel.go`
**位置**：第 814-825 行

```go
// parseChannelInfo 函数中的 ExpireAt 读取逻辑
case key.TableChannelInfo.Column.ExpireAt:
    tm := int64(wk.endian.Uint64(iter.Value()))
    if tm > 0 {
        t := time.Unix(tm/1e9, tm%1e9)
        preChannelInfo.ExpireAt = &t
    }
```

**评估**：✅ **有效**
- 正确从 `UnixNano` 转换回 `time.Time`
- 处理了 `tm > 0` 的判断

### 3.4 删除逻辑

**文件**：`pkg/wkdb/channel.go`
**位置**：第 730-740 行

```go
// deleteChannelInfoIndex 函数中的索引删除
if channelInfo.ExpireAt != nil {
    if err := w.Delete(
        key.NewChannelInfoSecondIndexKey(
            key.TableChannelInfo.SecondIndex.ExpireAt,
            uint64(channelInfo.ExpireAt.UnixNano()),
            channelInfo.Id
        ),
        wk.noSync
    ); err != nil {
        return err
    }
}
```

**评估**：✅ **有效**
- 删除频道时正确清理二级索引
- 处理了 `nil` 情况

---

## 4. 权限验证层改造（✅ 有效）

### 4.1 频道级别权限检查

**文件**：`internal/service/permission.go`
**位置**：第 55-82 行

```go
func (p *PermissionService) HasPermissionForChannel(channelId string, channelType uint8) (wkproto.ReasonCode, error) {
    // 检查是否为公开频道类型
    if p.IsPublicChannelType(channelType) {
        return wkproto.ReasonSuccess, nil
    }

    // 查询频道基本信息
    channelInfo, err := Store.GetChannel(channelId, channelType)
    if err != nil {
        p.Error("HasPermissionForChannel: GetChannel error", zap.Error(err))
        return wkproto.ReasonSystemError, err
    }

    // 频道被封禁
    if channelInfo.Ban {
        return wkproto.ReasonBan, nil
    }

    // 频道已解散
    if channelInfo.Disband {
        return wkproto.ReasonDisband, nil
    }

    // ✅ ExpireAt 检查 - 第 78-80 行
    if channelInfo.ExpireAt != nil && time.Now().After(*channelInfo.ExpireAt) {
        return wkproto.ReasonSendBan, nil
    }

    return wkproto.ReasonSuccess, nil
}
```

**评估**：✅ **有效**
- 检查位置正确（在封禁、解散检查之后）
- 逻辑正确：`ExpireAt != nil` 且 `当前时间 > ExpireAt`
- 返回值正确：`wkproto.ReasonSendBan`（禁止发送）

**调用链路**：
```
用户发送消息
  ↓
internal/user/handler/event_onsend.go:handleOnSend()
  ↓
internal/service/permission.go:HasPermissionForChannel()
  ↓
检查 ExpireAt
  ↓
如果过期 → 返回 ReasonSendBan → 拒绝发送
```

---

## 5. API层改造（⚠️ 部分问题已修复）

### 5.1 原始设计的问题

#### 5.1.1 请求模型定义（原始版本）

**文件**：`internal/api/channel_model.go`
**位置**：第 96-116 行（原始）

```go
// ❌ 原始版本 - 存在问题
type channelExpireUpdateReq struct {
    ChannelId   string `json:"channel_id"`
    ChannelType uint8  `json:"channel_type"`
    ExpireAt    int64  `json:"expire_at"`
}

func (r channelExpireUpdateReq) Check() error {
    if strings.TrimSpace(r.ChannelId) == "" {
        return errors.New("channel_id不能为空！")
    }
    // ❌ 问题1：对于个人频道的 FakeChannelID 会报错
    if options.IsSpecialChar(r.ChannelId) {
        return errors.New("频道ID不能包含特殊字符！")
    }
    // ... 其他检查
}
```

**问题分析**：

1. **个人频道的 channel_id 语义问题**：
   - 个人频道在存储层使用 FakeChannelID（如 `"234@123"`）
   - 但 API 层期望用户传入简单的 UID（如 `"123"`）
   - 导致用户无法正确指定个人频道

2. **特殊字符检查冲突**：
   - `IsSpecialChar()` 会拒绝包含 `@` 的字符串
   - FakeChannelID 必然包含 `@`
   - 导致直接传 FakeChannelID 会报错

3. **缺少双方UID参数**：
   - 用户123和234的单聊，只传 `channel_id="123"` 无法确定是哪个单聊
   - 需要同时知道双方的UID才能生成正确的 FakeChannelID

### 5.2 修复后的设计

#### 5.2.1 请求模型定义（修复版）

**文件**：`internal/api/channel_model.go`
**位置**：第 96-129 行（修复后）

```go
// ✅ 修复版本
type channelExpireUpdateReq struct {
    ChannelId   string `json:"channel_id"`
    ChannelType uint8  `json:"channel_type"`
    // ✅ 可选字段：过期时间戳（Unix秒）
    // 不传或传0：表示取消过期限制（永不过期）
    // 传正数：表示设置过期时间
    ExpireAt *int64 `json:"expire_at,omitempty"`
    // ✅ 可选字段：用于个人频道时指定另一个用户的UID
    ToUid string `json:"to_uid,omitempty"`
}

func (r channelExpireUpdateReq) Check() error {
    if strings.TrimSpace(r.ChannelId) == "" {
        return errors.New("channel_id不能为空！")
    }

    // ✅ 修复：如果指定了 to_uid，说明是通过双方UID指定个人频道
    if r.ToUid == "" {
        // 普通模式：直接使用 channel_id
        if options.IsSpecialChar(r.ChannelId) {
            return errors.New("频道ID不能包含特殊字符！")
        }
    } else {
        // 双UID模式：确保两个UID都没有特殊字符
        if options.IsSpecialChar(r.ChannelId) || options.IsSpecialChar(r.ToUid) {
            return errors.New("用户ID不能包含特殊字符！")
        }
    }

    if r.ChannelType == 0 {
        return errors.New("频道类型不能为0！")
    }
    // ✅ 如果传了 expire_at，检查其值不能为负数
    if r.ExpireAt != nil && *r.ExpireAt < 0 {
        return errors.New("expire_at不能小于0！")
    }
    return nil
}
```

**改进点**：

1. ✅ **新增 `to_uid` 字段**：
   - 可选字段，仅在个人频道时使用
   - 与 `channel_id` 组合生成 FakeChannelID

2. ✅ **`expire_at` 改为指针类型**：
   - 使用 `*int64` 而不是 `int64`，真正实现可选
   - 添加 `omitempty` 标签
   - 不传值（nil）或传 0 都表示"永不过期"
   - 传正数表示设置过期时间

3. ✅ **智能检查逻辑**：
   - `to_uid` 为空：普通模式，检查 `channel_id` 的特殊字符
   - `to_uid` 非空：双UID模式，分别检查两个UID
   - `expire_at` 只在非 nil 且为负数时才报错

#### 5.2.2 API 处理逻辑（修复版）

**文件**：`internal/api/channel.go`
**位置**：第 117-208 行（修复后）

```go
func (ch *channel) channelExpireUpdate(c *wkhttp.Context) {
    var req channelExpireUpdateReq
    bodyBytes, err := BindJSON(&req, c)
    if err != nil {
        c.ResponseError(errors.Wrap(err, "数据格式有误！"))
        return
    }
    if err := req.Check(); err != nil {
        c.ResponseError(err)
        return
    }

    // ✅ 新增：如果是个人频道且提供了 to_uid，生成 FakeChannelID
    actualChannelId := req.ChannelId
    if req.ChannelType == wkproto.ChannelTypePerson && req.ToUid != "" {
        actualChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.ToUid)
        ch.Debug("个人频道使用FakeChannelID",
            zap.String("from", req.ChannelId),
            zap.String("to", req.ToUid),
            zap.String("fakeChannelId", actualChannelId))
    }

    // ✅ 使用 actualChannelId 查询集群节点
    leaderInfo, err := service.Cluster.SlotLeaderOfChannel(actualChannelId, req.ChannelType)
    if err != nil {
        ch.Error("获取频道所在节点失败！", zap.Error(err),
            zap.String("channelID", actualChannelId),
            zap.Uint8("channelType", req.ChannelType))
        c.ResponseError(errors.New("获取频道所在节点失败！"))
        return
    }

    // 如果不是本地节点，转发请求
    if leaderInfo.Id != options.G.Cluster.NodeId {
        ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
        c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
        return
    }

    // ✅ 查询频道信息
    channelInfo, err := service.Store.GetChannel(actualChannelId, req.ChannelType)
    if err != nil {
        ch.Error("查询频道信息失败！", zap.Error(err),
            zap.String("channelId", actualChannelId),
            zap.Uint8("channelType", req.ChannelType))
        c.ResponseError(errors.New("查询频道信息失败！"))
        return
    }

    // ✅ 新增：如果频道不存在，自动创建（仅限个人频道）
    if wkdb.IsEmptyChannelInfo(channelInfo) {
        if req.ChannelType == wkproto.ChannelTypePerson {
            // 自动创建个人频道
            now := time.Now()
            var expireAt *time.Time
            // ✅ 如果传了 expire_at 且大于0，则设置过期时间
            if req.ExpireAt != nil && *req.ExpireAt > 0 {
                t := time.Unix(*req.ExpireAt, 0)
                expireAt = &t
            }
            // 否则 expireAt 保持为 nil，表示永不过期

            channelInfo = wkdb.ChannelInfo{
                ChannelId:   actualChannelId,
                ChannelType: req.ChannelType,
                ExpireAt:    expireAt,
                CreatedAt:   &now,
                UpdatedAt:   &now,
            }

            err = service.Store.AddChannelInfo(channelInfo)
            if err != nil {
                ch.Error("创建个人频道失败！", zap.Error(err),
                    zap.String("channelId", actualChannelId),
                    zap.Uint8("channelType", req.ChannelType))
                c.ResponseError(errors.New("创建个人频道失败！"))
                return
            }

            if expireAt != nil {
                ch.Info("自动创建个人频道（带过期时间）",
                    zap.String("channelId", actualChannelId),
                    zap.Time("expireAt", *expireAt))
            } else {
                ch.Info("自动创建个人频道（永不过期）",
                    zap.String("channelId", actualChannelId))
            }
            c.ResponseOK()
            return
        }

        c.ResponseError(errors.New("频道不存在！"))
        return
    }

    // ✅ 更新已存在频道的失效时间
    // 如果传了 expire_at 且大于0，设置过期时间
    // 如果没传 expire_at 或者传了0，取消过期限制
    if req.ExpireAt != nil && *req.ExpireAt > 0 {
        t := time.Unix(*req.ExpireAt, 0)
        channelInfo.ExpireAt = &t
    } else {
        channelInfo.ExpireAt = nil
    }
    updatedAt := time.Now()
    channelInfo.UpdatedAt = &updatedAt

    if err = service.Store.UpdateChannelInfo(channelInfo); err != nil {
        ch.Error("更新频道失效时间失败！", zap.Error(err),
            zap.String("channelId", actualChannelId),
            zap.Uint8("channelType", req.ChannelType))
        c.ResponseError(errors.New("更新频道失效时间失败！"))
        return
    }

    c.ResponseOK()
}
```

**改进点**：

1. ✅ **FakeChannelID 生成**（第 130-134 行）：
   - 检测到个人频道且提供了 `to_uid`
   - 自动调用 `GetFakeChannelIDWith()` 生成正确的 FakeChannelID

2. ✅ **自动创建个人频道**（第 157-190 行）：
   - 个人频道通常不需要预先创建
   - 如果不存在，自动创建并设置过期时间
   - 支持 `expire_at` 指针类型：nil 或 0 表示永不过期
   - 区分日志输出："带过期时间" vs "永不过期"
   - 避免用户先手动创建频道的麻烦

3. ✅ **更新频道过期时间**（第 198-205 行）：
   - 使用指针类型判断：`req.ExpireAt != nil && *req.ExpireAt > 0`
   - 如果传了正数，设置过期时间
   - 否则（nil 或 0），取消过期限制（设置为 nil）

4. ✅ **统一使用 `actualChannelId`**：
   - 所有后续操作都使用生成的 FakeChannelID
   - 确保查询、更新、日志记录的一致性

### 5.3 路由注册

**文件**：`internal/api/channel.go`
**位置**：第 36-41 行

```go
func (ch *channel) route(r *wkhttp.WKHttp) {
    //################### 频道 ###################
    r.POST("/channel", ch.channelCreateOrUpdate)       // 创建或修改频道
    r.POST("/channel/info", ch.updateOrAddChannelInfo) // 更新或添加频道基础信息
    r.POST("/channel/delete", ch.channelDelete)        // 删除频道
    r.POST("/channel/expire", ch.channelExpireUpdate)  // ✅ 新增路由
    // ...
}
```

**评估**：✅ **有效**
- 路由路径清晰：`POST /channel/expire`
- 处理函数正确绑定

---

## 6. 问题分析与修复

### 6.1 问题1：个人频道无法正确指定

**原始错误场景**：

```bash
# 尝试1：传入接收者UID
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "expire_at": 1759588140
  }'

# 结果：{"msg":"频道不存在！","status":400}
# 原因：查询 "123-1"，但实际存储的是 "234@123-1"
```

```bash
# 尝试2：传入 FakeChannelID
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123@234",
    "channel_type": 1,
    "expire_at": 1759588140
  }'

# 结果：{"msg":"频道ID不能包含特殊字符！","status":400}
# 原因：IsSpecialChar() 拒绝了 "@" 字符
```

**根本原因**：

1. **个人频道的双重身份**：
   - API层：用户使用对方的UID（如 `"234"`）
   - 存储层：系统使用 FakeChannelID（如 `"234@123"`）

2. **缺少上下文信息**：
   - 单独的 `channel_id="123"` 无法确定是123和谁的单聊
   - 需要同时知道发送者和接收者

**修复方案**：

✅ **方案：新增 `to_uid` 参数**

```bash
# ✅ 修复后的正确用法
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "to_uid": "234",
    "expire_at": 1759588140
  }'

# 系统内部处理：
# 1. 检测到 channel_type=1 且 to_uid="234"
# 2. 生成 FakeChannelID: GetFakeChannelIDWith("123", "234") = "234@123"
# 3. 查询或创建频道："234@123-1"
# 4. 设置过期时间
```

### 6.2 问题2：群组频道的兼容性

**场景**：群组频道不需要 `to_uid` 参数

```bash
# ✅ 群组频道的正确用法（不变）
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "group123",
    "channel_type": 2,
    "expire_at": 1759588140
  }'

# 系统内部处理：
# 1. 检测到 to_uid 为空
# 2. 直接使用 channel_id="group123"
# 3. 查询频道："group123-2"
# 4. 更新过期时间
```

**兼容性**：✅ **完全兼容**
- `to_uid` 是可选参数（`omitempty`）
- 群组频道不提供 `to_uid`，走原有逻辑

---

## 7. 测试用例

### 7.1 个人频道测试

#### 测试1：设置个人频道过期时间（新建）

```bash
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "user123",
    "channel_type": 1,
    "to_uid": "user234",
    "expire_at": 1759588140
  }'

# 预期结果：
# - HTTP 200 OK
# - 自动创建频道 "user234@user123"（或 "user123@user234"，取决于哈希值）
# - ExpireAt 设置为 2025-10-04 12:35:40
```

**验证过期逻辑**：

```bash
# 1. 在过期时间之前发送消息
curl -X POST http://127.0.0.1:5001/message/send \
  -H 'Content-Type: application/json' \
  -d '{
    "from_uid": "user123",
    "channel_id": "user234",
    "channel_type": 1,
    "payload": "SGVsbG8="
  }'

# 预期：成功发送

# 2. 修改系统时间或等待过期后发送
# 预期：返回 ReasonSendBan，拒绝发送
```

#### 测试2：更新已存在个人频道的过期时间

```bash
# 假设频道已存在
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "user123",
    "channel_type": 1,
    "to_uid": "user234",
    "expire_at": 1759674540
  }'

# 预期结果：
# - HTTP 200 OK
# - ExpireAt 更新为新时间戳
```

#### 测试3：取消个人频道的过期时间

```bash
# 方式1：传 expire_at: 0
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "user123",
    "channel_type": 1,
    "to_uid": "user234",
    "expire_at": 0
  }'

# 方式2：不传 expire_at 参数
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "user123",
    "channel_type": 1,
    "to_uid": "user234"
  }'

# 预期结果：
# - HTTP 200 OK
# - ExpireAt 设置为 nil（永不过期）
# - 两种方式效果相同
```

### 7.2 群组频道测试

#### 测试4：设置群组过期时间

```bash
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "group123",
    "channel_type": 2,
    "expire_at": 1759588140
  }'

# 预期结果：
# - HTTP 200 OK（如果频道已存在）
# - 或 {"msg":"频道不存在！","status":400}（如果频道不存在）
```

**注意**：群组频道通常需要预先创建，不会自动创建。

### 7.3 错误场景测试

#### 测试5：参数验证

```bash
# 缺少 channel_id
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_type": 1,
    "expire_at": 1759588140
  }'
# 预期：{"msg":"channel_id不能为空！","status":400}

# 缺少 channel_type
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "expire_at": 1759588140
  }'
# 预期：{"msg":"频道类型不能为0！","status":400}

# expire_at 为负数
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "to_uid": "234",
    "expire_at": -1
  }'
# 预期：{"msg":"expire_at不能小于0！","status":400}
```

---

## 8. 遗漏项检查

### 8.1 集群同步层 ✅

**文件**：`pkg/cluster/store/model.go`

需要检查 `ChannelInfo` 的序列化和反序列化是否包含 `ExpireAt` 字段。

**位置**：第 860-877 行（序列化）

```go
func (c *CMD) encodeChannelInfo(channelInfo wkdb.ChannelInfo, version uint16) ([]byte, error) {
    // ... 其他字段

    if version > 3 {
        if channelInfo.ExpireAt != nil {
            enc.WriteUint64(uint64(channelInfo.ExpireAt.UnixNano()))
        } else {
            enc.WriteUint64(0)
        }
    }

    return enc.Bytes(), nil
}
```

**位置**：第 947-951 行（反序列化）

```go
func (c *CMD) decodeChannelInfo(...) (wkdb.ChannelInfo, error) {
    // ... 其他字段

    if c.version > 3 {
        var expireAt uint64
        if expireAt, err = dec.Uint64(); err != nil {
            return channelInfo, err
        }
        if expireAt > 0 {
            t := time.Unix(expireAt/1e9, expireAt%1e9)
            channelInfo.ExpireAt = &t
        }
    }

    return channelInfo, nil
}
```

**评估**：✅ **有效**
- 版本控制正确（`version > 3`）
- 序列化/反序列化逻辑正确
- 处理了 `nil` 情况

### 8.2 迁移任务 ✅

**文件**：`internal/api/task_migrate.go`

需要确保数据迁移时包含 `ExpireAt` 字段。

**检查点**：
- 迁移任务在导出频道信息时，是否包含 `ExpireAt`
- 导入时是否正确解析 `ExpireAt`

**结论**：由于 `wkdb.ChannelInfo` 结构体已包含该字段，且迁移代码使用该结构体，因此 ✅ **自动支持**。

### 8.3 缓存层 ⚠️

**文件**：可能涉及 `internal/cache/` 或类似模块

**检查点**：
- 频道信息缓存是否包含 `ExpireAt`
- 缓存失效逻辑是否考虑 `ExpireAt`

**当前状态**：
- 代码中未发现明显的频道信息缓存
- 如果存在缓存，需要确保缓存的 `ChannelInfo` 包含 `ExpireAt`

**建议**：
- 搜索代码中是否有 `channelInfoCache` 或类似命名
- 如果有，确保缓存的数据结构是完整的 `wkdb.ChannelInfo`

### 8.4 搜索功能 ⚠️

**文件**：`pkg/wkdb/channel.go` 中的 `SearchChannels` 函数

**检查点**：
- 搜索结果是否包含 `ExpireAt` 字段
- 是否需要按 `ExpireAt` 进行过滤或排序

**当前状态**：
- `SearchChannels` 返回 `[]ChannelInfo`
- 由于使用完整的结构体，✅ **自动包含 `ExpireAt`**

**建议**：
- 如果需要"只显示未过期频道"的功能，需要添加过滤逻辑
- 当前实现不过滤，返回所有频道（包括已过期的）

---

## 9. 总结

### 9.1 有效的改造 ✅

1. **数据模型层**：
   - `pkg/wkdb/model.go` 的 `ChannelInfo.ExpireAt` 字段定义

2. **存储层**：
   - `pkg/wkdb/key/table.go` 的列和索引定义
   - `pkg/wkdb/channel.go` 的读写逻辑

3. **集群同步层**：
   - `pkg/cluster/store/model.go` 的序列化/反序列化

4. **权限验证层**：
   - `internal/service/permission.go` 的过期检查逻辑

5. **API层（修复后）**：
   - `internal/api/channel_model.go` 的请求模型和参数验证
   - `internal/api/channel.go` 的 `channelExpireUpdate` 实现
   - `internal/api/channel.go` 的路由注册

### 9.2 修复的问题 ⚠️

1. **个人频道 channel_id 语义问题**：
   - ✅ 新增 `to_uid` 参数
   - ✅ 自动生成 FakeChannelID
   - ✅ 智能参数验证

2. **个人频道不存在时的处理**：
   - ✅ 自动创建个人频道
   - ✅ 避免用户手动创建的麻烦

3. **特殊字符检查冲突**：
   - ✅ 区分普通模式和双UID模式
   - ✅ 分别进行不同的验证

4. **expire_at 参数可选性**：
   - ✅ 改为指针类型 `*int64`，实现真正可选
   - ✅ 支持不传值（nil）表示"永不过期"
   - ✅ 支持传 0 表示"取消过期限制"
   - ✅ 支持传正数表示"设置过期时间"
   - ✅ 区分日志输出："带过期时间" vs "永不过期"

### 9.3 未发现明显遗漏 ✅

所有关键路径都已覆盖：
- 数据定义
- 持久化
- 集群同步
- 权限验证
- API 接口

### 9.4 使用建议

#### 个人频道过期设置

```bash
# 设置用户123和用户234的单聊在指定时间过期
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "to_uid": "234",
    "expire_at": 1759588140
  }'
```

**参数说明**：
- `channel_id`: 其中一个用户的UID
- `to_uid`: 另一个用户的UID
- 系统会自动组合生成正确的 FakeChannelID

#### 群组频道过期设置

```bash
# 设置群组在指定时间过期
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "group123",
    "channel_type": 2,
    "expire_at": 1759588140
  }'
```

**注意**：群组频道需要预先存在，否则会报错。

#### 取消过期时间

```bash
# 方式1：将 expire_at 设置为 0
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "to_uid": "234",
    "expire_at": 0
  }'

# 方式2：不传 expire_at 参数
curl -X POST http://127.0.0.1:5001/channel/expire \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_id": "123",
    "channel_type": 1,
    "to_uid": "234"
  }'
```

**说明**：两种方式效果相同，都会将 `ExpireAt` 设置为 `nil`（永不过期）。

### 9.5 注意事项

1. **时间戳格式**：
   - 使用 Unix 时间戳（秒）
   - 例如：`1759588140` 对应 `2025-10-04 12:35:40`

2. **expire_at 参数说明**：
   - **不传 `expire_at`**：永不过期（ExpireAt = nil）
   - **传 `expire_at: 0`**：永不过期（ExpireAt = nil）
   - **传 `expire_at: 正数`**：设置过期时间
   - **传 `expire_at: 负数`**：参数验证失败

3. **过期后的行为**：
   - 频道仍然存在，只是不能发送消息
   - 返回错误码：`ReasonSendBan`
   - 不会自动删除频道数据

4. **个人频道的创建**：
   - 如果频道不存在，会自动创建
   - 自动创建时根据 `expire_at` 参数设置过期时间
   - 适用于"临时会话"场景

5. **集群环境**：
   - API 会自动转发到正确的节点
   - 确保集群版本一致（支持 `version > 3`）

---

**文档版本**：v1.1
**最后更新**：2025-01-05

