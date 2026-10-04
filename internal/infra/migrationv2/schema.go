package migrationv2

// Schema IDs and scalar widths follow pkg/wkdb/key/table.go and its writers at
// SourceCommit. Keep numeric IDs stable; unknown primary columns fail closed.
type columnSpec struct {
	name string
	size int
}
type tableSpec struct {
	name     string
	keyBytes int
	columns  map[uint16]columnSpec
}

var tables = map[uint16]tableSpec{
	0x0101: {name: "Message", keyBytes: 22, columns: map[uint16]columnSpec{
		0x0101: {name: "Header", size: 1},
		0x0102: {name: "Setting", size: 1},
		0x0103: {name: "Expire", size: 4},
		0x0104: {name: "MessageId", size: 8},
		0x0105: {name: "MessageSeq", size: 8},
		0x0106: {name: "ClientMsgNo", size: 0},
		0x0107: {name: "Timestamp", size: 4},
		0x0108: {name: "ChannelId", size: 0},
		0x0109: {name: "ChannelType", size: 1},
		0x010a: {name: "Topic", size: 0},
		0x010b: {name: "FromUid", size: 0},
		0x010c: {name: "Payload", size: 0},
		0x010d: {name: "Term", size: 8},
		0x010e: {name: "StreamNo", size: 0},
	}},
	0x0201: {name: "User", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0201: {name: "Uid", size: 0},
		0x0202: {name: "DeviceCount", size: 4},
		0x0203: {name: "OnlineDeviceCount", size: 4},
		0x0204: {name: "ConnCount", size: 4},
		0x0205: {name: "SendMsgCount", size: 8},
		0x0206: {name: "RecvMsgCount", size: 8},
		0x0207: {name: "SendMsgBytes", size: 8},
		0x0208: {name: "RecvMsgBytes", size: 8},
		0x0209: {name: "CreatedAt", size: 8},
		0x020a: {name: "UpdatedAt", size: 8},
		0x020b: {name: "PluginNo", size: 0},
	}},
	0x0301: {name: "Device", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0301: {name: "Uid", size: 0},
		0x0302: {name: "Token", size: 0},
		0x0303: {name: "DeviceFlag", size: 8},
		0x0304: {name: "DeviceLevel", size: 1},
		0x0305: {name: "CreatedAt", size: 8},
		0x0306: {name: "UpdatedAt", size: 8},
	}},
	0x0401: {name: "Subscriber", keyBytes: 22, columns: map[uint16]columnSpec{
		0x0401: {name: "Uid", size: 0},
		0x0402: {name: "CreatedAt", size: 8},
		0x0403: {name: "UpdatedAt", size: 8},
	}},
	0x0501: {name: "SubscriberChannelRelation", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0501: {name: "Channel", size: 0},
	}},
	0x0601: {name: "ChannelInfo", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0601: {name: "Id", size: 0},
		0x0602: {name: "ChannelId", size: 0},
		0x0603: {name: "ChannelType", size: 1},
		0x0604: {name: "Ban", size: 1},
		0x0605: {name: "Large", size: 1},
		0x0606: {name: "Disband", size: 1},
		0x0607: {name: "SubscriberCount", size: 4},
		0x0608: {name: "AllowlistCount", size: 4},
		0x0609: {name: "DenylistCount", size: 4},
		0x060a: {name: "CreatedAt", size: 8},
		0x060b: {name: "UpdatedAt", size: 8},
		0x060c: {name: "SendBan", size: 1},
		0x060d: {name: "AllowStranger", size: 1},
	}},
	0x0701: {name: "Denylist", keyBytes: 22, columns: map[uint16]columnSpec{
		0x0701: {name: "Uid", size: 0},
		0x0702: {name: "CreatedAt", size: 8},
		0x0703: {name: "UpdatedAt", size: 8},
	}},
	0x0801: {name: "Allowlist", keyBytes: 22, columns: map[uint16]columnSpec{
		0x0801: {name: "Uid", size: 0},
		0x0802: {name: "CreatedAt", size: 8},
		0x0803: {name: "UpdatedAt", size: 8},
	}},
	0x0901: {name: "Conversation", keyBytes: 22, columns: map[uint16]columnSpec{
		0x0901: {name: "Uid", size: 0},
		0x0902: {name: "ChannelId", size: 0},
		0x0903: {name: "ChannelType", size: 1},
		0x0904: {name: "Type", size: 1},
		0x0905: {name: "UnreadCount", size: 4},
		0x0906: {name: "ReadedToMsgSeq", size: 8},
		0x0907: {name: "CreatedAt", size: 8},
		0x0908: {name: "UpdatedAt", size: 8},
		0x0909: {name: "DeletedAtMsgSeq", size: 8},
	}},
	0x0a01: {name: "MessageNotifyQueue", keyBytes: 12, columns: map[uint16]columnSpec{}},
	0x0b01: {name: "ChannelClusterConfig", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0b01: {name: "ChannelId", size: 0},
		0x0b02: {name: "ChannelType", size: 1},
		0x0b03: {name: "ReplicaMaxCount", size: 2},
		0x0b04: {name: "Replicas", size: -8},
		0x0b05: {name: "Learners", size: -8},
		0x0b06: {name: "LeaderId", size: 8},
		0x0b07: {name: "Term", size: 4},
		0x0b08: {name: "MigrateFrom", size: 8},
		0x0b09: {name: "MigrateTo", size: 8},
		0x0b0a: {name: "Status", size: 1},
		0x0b0b: {name: "ConfVersion", size: 8},
		0x0b0c: {name: "Version", size: 2},
		0x0b0d: {name: "CreatedAt", size: 8},
		0x0b0e: {name: "UpdatedAt", size: 8},
	}},
	0x0c01: {name: "LeaderTermSequence", keyBytes: 16, columns: map[uint16]columnSpec{}},
	0x0d01: {name: "ChannelCommon", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0d01: {name: "AppliedIndex", size: 8},
	}},
	0x0f01: {name: "Total", keyBytes: 14, columns: map[uint16]columnSpec{
		0x0f01: {name: "User", size: 8},
		0x0f02: {name: "Device", size: 8},
		0x0f03: {name: "Session", size: 8},
		0x0f04: {name: "Conversation", size: 8},
		0x0f05: {name: "Message", size: 8},
		0x0f06: {name: "Channel", size: 8},
		0x0f07: {name: "ChannelClusterConfig", size: 8},
	}},
	0x1001: {name: "SystemUid", keyBytes: 14, columns: map[uint16]columnSpec{
		0x1001: {name: "Uid", size: 0},
	}},
	// Pre-2026-03-07 storage can survive an in-place original v2 upgrade.
	// These raw business records require a named exclusion, never silent skip.
	0x1101: {name: "LegacyStream", keyBytes: 22},
	0x1201: {name: "LegacyStreamMeta", keyBytes: 12},
	0x1301: {name: "ConversationLocalUser", keyBytes: 0, columns: map[uint16]columnSpec{}},
	0x1401: {name: "Tester", keyBytes: 14, columns: map[uint16]columnSpec{
		0x1401: {name: "No", size: 0},
		0x1402: {name: "Addr", size: 0},
		0x1403: {name: "CreatedAt", size: 8},
		0x1404: {name: "UpdatedAt", size: 8},
	}},
	0x1501: {name: "Plugin", keyBytes: 14, columns: map[uint16]columnSpec{
		0x1501: {name: "No", size: 0},
		0x1502: {name: "Name", size: 0},
		0x1503: {name: "ConfigTemplate", size: 0},
		0x1504: {name: "CreatedAt", size: 8},
		0x1505: {name: "UpdatedAt", size: 8},
		0x1506: {name: "Status", size: 4},
		0x1507: {name: "Version", size: 0},
		0x1508: {name: "Methods", size: 0},
		0x1509: {name: "Priority", size: 4},
		0x150a: {name: "Config", size: 0},
	}},
	0x1601: {name: "PluginUser", keyBytes: 14, columns: map[uint16]columnSpec{
		0x1601: {name: "PluginNo", size: 0},
		0x1602: {name: "Uid", size: 0},
		0x1603: {name: "CreatedAt", size: 8},
		0x1604: {name: "UpdatedAt", size: 8},
	}},
	0x1801: {name: "MessageEvent", keyBytes: 30, columns: map[uint16]columnSpec{}},
	0x1901: {name: "MessageEventState", keyBytes: 28, columns: map[uint16]columnSpec{}},
	0x1a01: {name: "MessageEventSeq", keyBytes: 20, columns: map[uint16]columnSpec{}},
}
