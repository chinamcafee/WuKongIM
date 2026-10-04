package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// catalog stores every distinct hint under a value digest, then publishes only
// unambiguous groups. Conflicting hints cannot overwrite one another.
func (d *diagnostician) catalog(capture SourceCapture) error {
	hint := func(key string, value []byte) error {
		return d.batch.add(transfer.SpoolRow{Key: []byte("diagnostic/hints/" + key + "/" + diagnosticSHA(value)), Value: value})
	}
	for _, node := range capture.Nodes {
		if err := walkSourceRows(d.ctx, d.w, node.NodeID, func(row Row) error {
			id, err := d.decoder.Identify(row)
			if err != nil {
				return nil
			} // The row census emits this once, with provenance.
			if id.UID != "" {
				if err := hint(fmt.Sprintf("catalog/uid/%016x", id.UIDHash), []byte(id.UID)); err != nil {
					return err
				}
			}
			if id.UIDPersonalChannelHash != 0 && id.UID != "" {
				data, err := MarshalState(ChannelIdentity{ID: id.UID, Type: 1})
				if err != nil {
					return err
				}
				if err := hint(fmt.Sprintf("catalog/channel/%016x", id.UIDPersonalChannelHash), data); err != nil {
					return err
				}
			}
			if id.Channel.ID != "" {
				data, err := MarshalState(id.Channel)
				if err != nil {
					return err
				}
				for _, key := range []string{fmt.Sprintf("catalog/channel/%016x", id.ChannelHash), fmt.Sprintf("catalog/event-channel/%016x", id.EventChannelHash)} {
					if err := hint(key, data); err != nil {
						return err
					}
				}
				if id.ClientMsgNo != "" {
					if err := hint(fmt.Sprintf("catalog/event-message/%016x/%016x", id.EventChannelHash, id.ClientMsgHash), []byte(id.ClientMsgNo)); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := d.batch.flush(); err != nil {
		return err
	}
	var group string
	var value []byte
	var count uint64
	flush := func() error {
		if group == "" {
			return nil
		}
		if count != 1 {
			return d.emit(DiagnosticFinding{Code: "identity.hint_collision", KeySHA256: diagnosticSHA([]byte(group)), Count: count})
		}
		return d.batch.add(transfer.SpoolRow{Key: []byte(group), Value: value})
	}
	err := d.w.Walk(d.ctx, []byte("diagnostic/hints/"), func(row transfer.SpoolRow) error {
		suffix := strings.TrimPrefix(string(row.Key), "diagnostic/hints/")
		key := suffix[:strings.LastIndexByte(suffix, '/')]
		if key != group {
			if err := flush(); err != nil {
				return err
			}
			group = key
			count = 0
		}
		count++
		value = append([]byte(nil), row.Value...)
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return d.batch.flush()
}

// diagnosticUnique keeps a distinct physical suffix. This permits an exhaustive
// sorted duplicate census with constant memory even when one group is huge.
func (d *diagnostician) unique(kind string, node uint64, key string, row Row) error {
	return d.put(fmt.Sprintf("diagnostic/unique/%s/%020d/%s/%s", kind, node, diagnosticSHA([]byte(key)), diagnosticRef(node, row).KeySHA256), diagnosticRef(node, row))
}

func diagnosticGet[T any](ctx context.Context, w Workspace, key string) (value T, found bool, err error) {
	data, found, err := w.Get(ctx, []byte(key))
	if err != nil || !found {
		return value, found, err
	}
	err = UnmarshalState(data, &value)
	return
}
