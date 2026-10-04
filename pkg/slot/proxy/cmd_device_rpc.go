package proxy

import (
	"context"
	"encoding/json"
	"errors"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"sort"
)

const cmdDeviceRPCServiceID = clusternet.RPCSlotCMDDeviceCursors
const cmdDeviceReadLimit = 512

type cmdDeviceReadRequest struct {
	SlotID uint64
	UID    string
	Keys   []metadb.CMDDeviceCursorKey
}
type cmdDeviceReadResponse struct {
	Status   string
	LeaderID uint64
	Rows     []metadb.CMDDeviceCursor
}

func (r cmdDeviceReadResponse) rpcStatus() string   { return r.Status }
func (r cmdDeviceReadResponse) rpcLeaderID() uint64 { return r.LeaderID }
func decodeCMDDeviceReadResponse(body []byte) (cmdDeviceReadResponse, error) {
	var r cmdDeviceReadResponse
	err := json.Unmarshal(body, &r)
	return r, err
}

// GetCMDDeviceCursor reads one independent device cursor from the current UID Slot leader.
func (s *Store) GetCMDDeviceCursor(ctx context.Context, key metadb.CMDDeviceCursorKey) (metadb.CMDDeviceCursor, bool, error) {
	rows, err := s.GetCMDDeviceCursorsBatch(ctx, []metadb.CMDDeviceCursorKey{key})
	r, ok := rows[key]
	return r, ok, err
}

// GetCMDDeviceCursorsBatch bounds each owner request and never uses an ingress replica as authority.
func (s *Store) GetCMDDeviceCursorsBatch(ctx context.Context, keys []metadb.CMDDeviceCursorKey) (map[metadb.CMDDeviceCursorKey]metadb.CMDDeviceCursor, error) {
	out := make(map[metadb.CMDDeviceCursorKey]metadb.CMDDeviceCursor, len(keys))
	groups := make(map[string][]metadb.CMDDeviceCursorKey)
	for _, key := range keys {
		if key.UID == "" || key.ChannelID == "" || key.ChannelType <= 0 {
			return nil, metadb.ErrInvalidArgument
		}
		groups[key.UID] = append(groups[key.UID], key)
	}
	uids := make([]string, 0, len(groups))
	for uid := range groups {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		slot := s.cluster.SlotForKey(uid)
		group := groups[uid]
		for start := 0; start < len(group); start += cmdDeviceReadLimit {
			part := group[start:min(start+cmdDeviceReadLimit, len(group))]
			var rows []metadb.CMDDeviceCursor
			var err error
			if s.shouldServeSlotLocally(slot) {
				rows, err = s.readCMDDeviceRows(ctx, uid, part)
			} else {
				payload, e := json.Marshal(cmdDeviceReadRequest{SlotID: uint64(slot), UID: uid, Keys: part})
				if e != nil {
					return nil, e
				}
				resp, e := callAuthoritativeRPC(ctx, s, slot, cmdDeviceRPCServiceID, payload, decodeCMDDeviceReadResponse)
				rows, err = resp.Rows, e
			}
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				key := metadb.CMDDeviceCursorKey{UID: row.UID, DeviceFlag: row.DeviceFlag, ChannelID: row.ChannelID, ChannelType: row.ChannelType}
				out[key] = row
			}
		}
	}
	return out, nil
}
func (s *Store) readCMDDeviceRows(ctx context.Context, uid string, keys []metadb.CMDDeviceCursorKey) ([]metadb.CMDDeviceCursor, error) {
	rows := make([]metadb.CMDDeviceCursor, 0, len(keys))
	shard := s.db.ForHashSlot(hashSlotForKey(s.cluster, uid))
	for _, key := range keys {
		if key.UID != uid {
			return nil, metadb.ErrInvalidArgument
		}
		row, ok, err := shard.GetCMDDeviceCursor(ctx, key)
		if err != nil {
			return nil, err
		}
		if ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (s *Store) handleCMDDeviceReadRPC(ctx context.Context, body []byte) ([]byte, error) {
	if len(body) > 1024*1024 {
		return nil, metadb.ErrInvalidArgument
	}
	var req cmdDeviceReadRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if req.UID == "" || len(req.Keys) == 0 || len(req.Keys) > cmdDeviceReadLimit || uint64(s.cluster.SlotForKey(req.UID)) != req.SlotID {
		return nil, metadb.ErrInvalidArgument
	}
	if raw, handled, err := s.handleAuthoritativeRPC(multiraft.SlotID(req.SlotID), func(status string, leader uint64) ([]byte, error) {
		return json.Marshal(cmdDeviceReadResponse{Status: status, LeaderID: leader})
	}); handled || err != nil {
		return raw, err
	}
	rows, err := s.readCMDDeviceRows(ctx, req.UID, req.Keys)
	if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return nil, err
	}
	return json.Marshal(cmdDeviceReadResponse{Status: rpcStatusOK, Rows: rows})
}
