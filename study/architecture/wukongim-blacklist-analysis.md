## WuKongIM 白名单与黑名单优先级分析

### 权限检查优先级

黑名单优先级高于白名单。具体权限检查流程如下：

### 1. 通用频道权限检查流程 (internal/service/permission.go:131-179)

func (p *PermissionService) hasPermissionForCommChannel(channelId string, channelType uint8, sender SenderInfo) (wkproto.ReasonCode, error) {
    // 第一步：检查黑名单
    isDenylist, err := Store.ExistDenylist(realFakeChannelId, channelType, fromUid)
    if isDenylist {
        return wkproto.ReasonInBlacklist, nil  // 在黑名单中，直接拒绝
    }

    // 第二步：检查是否为订阅者
    isSubscriber, err := Store.ExistSubscriber(realFakeChannelId, channelType, fromUid)
    if !isSubscriber {
        return wkproto.ReasonSubscriberNotExist, nil  // 不是订阅者，拒绝
    }

    // 第三步：检查白名单（仅非个人频道）
    if channelType != wkproto.ChannelTypePerson {
        hasAllowlist, err := Store.HasAllowlist(realFakeChannelId, channelType)
        if hasAllowlist {
            isAllowlist, err := Store.ExistAllowlist(realFakeChannelId, channelType, fromUid)
            if !isAllowlist {
                return wkproto.ReasonNotInWhitelist, nil  // 不在白名单中，拒绝
            }
        }
    }

    return wkproto.ReasonSuccess, nil  // 通过所有检查，允许
}

### 2. 个人频道权限检查流程 (internal/service/permission.go:245-269)

func (p *PermissionService) allowSend(from, to string) (wkproto.ReasonCode, error) {
    // 第一步：检查黑名单
    isDenylist, err := Store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
    if isDenylist {
        return wkproto.ReasonInBlacklist, nil  // 在黑名单中，直接拒绝
    }

    // 第二步：检查白名单（如果启用）
    if !options.G.WhitelistOffOfPerson {
        isAllowlist, err := Store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
        if !isAllowlist {
            return wkproto.ReasonNotInWhitelist, nil  // 不在白名单中，拒绝
        }
    }

    return wkproto.ReasonSuccess, nil  // 通过所有检查，允许
}

### 3. 优先级总结

 检查顺序                          │ 检查项目                          │ 优先级                            │ 拒绝原因
───────────────────────────────────┼───────────────────────────────────┼───────────────────────────────────┼───────────────────────────────────
 1                                 │ 黑名单检查                        │ 最高                              │ ReasonInBlacklist
                                   │                                   │                                   │
 2                                 │ 订阅者检查                        │ 中等                              │ ReasonSubscriberNotExist
                                   │                                   │                                   │
 3                                 │ 白名单检查                        │ 最低                              │ ReasonNotInWhitelist
                                   │                                   │                                   │


### 4. 实际场景分析

#### 场景1：用户同时在黑名单和白名单中

• 结果：拒绝访问
• 原因：黑名单优先级更高，一旦在黑名单中就直接拒绝，不会继续检查白名单

#### 场景2：用户不在黑名单但不在白名单中

• 结果：拒绝访问（如果启用了白名单）
• 原因：通过黑名单检查，但在白名单检查时失败

#### 场景3：用户不在黑名单但在白名单中

• 结果：允许访问
• 原因：通过所有检查

#### 场景4：用户不在黑名单，频道没有设置白名单

• 结果：允许访问（如果是订阅者）
• 原因：通过黑名单检查，没有白名单则跳过白名单检查

### 5. 设计理念

这种优先级设计符合安全原则：

• 黑名单优先：确保被禁止的用户无法访问，即使被误添加到白名单
• 白名单兜底：在允许访问的基础上，进一步限制访问范围
• 防御性编程：避免配置冲突导致的安全漏洞

### 6. API使用示例

#### 添加黑名单

curl -X POST "http://localhost:5001/channel/blacklist_add" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "group123",
    "channel_type": 2,
    "uids": ["user456"]
  }'

#### 添加白名单

curl -X POST "http://localhost:5001/channel/whitelist_add" \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "group123",
    "channel_type": 2,
    "uids": ["user789"]
  }'

### 7. 最佳实践建议

1. 避免同时使用：尽量不同时对同一用户设置黑名单和白名单
2. 黑名单优先：当需要绝对禁止某用户访问时，使用黑名单
3. 白名单限制：当需要精确控制访问权限时，使用白名单
4. 定期清理：避免配置冲突，定期检查和清理冲突的权限设置

结论：黑名单优先级高于白名单，这是为了确保安全性和避免配置冲突导致的安全漏洞。