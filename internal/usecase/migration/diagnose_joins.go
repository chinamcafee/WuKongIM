package migration

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

func (d *diagnostician) joins() error {
	if err := d.uniqueGroups(); err != nil {
		return err
	}
	if err := d.indexGroups(); err != nil {
		return err
	}
	for _, node := range d.report.Nodes {
		if !node.Complete {
			continue
		}
		prefix := fmt.Sprintf("diagnostic/business/%020d/", node.NodeID)
		if err := d.logs(prefix); err != nil {
			return err
		}
		if err := d.w.Walk(d.ctx, []byte(prefix+"subscriber/"), func(row transfer.SpoolRow) error {
			var member struct {
				Ref    DiagnosticFinding
				Member SourceMember
			}
			if err := UnmarshalState(row.Value, &member); err != nil {
				return err
			}
			_, found, err := d.w.Get(d.ctx, []byte(prefix+"conversation/"+tuple(member.Member.UID, member.Member.Channel.ID, member.Member.Channel.Type)))
			if err != nil || found {
				return err
			}
			hasHistory := false
			err = d.w.Walk(d.ctx, []byte(prefix+"log/"+channelTuple(member.Member.Channel)+"/t/"), func(row transfer.SpoolRow) error {
				var tail diagnosticLogRow
				if err := UnmarshalState(row.Value, &tail); err != nil {
					return err
				}
				hasHistory = hasHistory || tail.Seq > 0
				return nil
			})
			if err != nil {
				return err
			}
			if hasHistory {
				member.Ref.Code = "conversation.visibility"
				return d.emit(member.Ref)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := d.events(prefix); err != nil {
			return err
		}
	}
	return nil
}

func (d *diagnostician) uniqueGroups() error {
	var group string
	var first DiagnosticFinding
	var count uint64
	flush := func() error {
		if count < 2 {
			return nil
		}
		first.Code = "duplicate." + strings.Split(group, "/")[2]
		first.Count = count
		return d.emit(first)
	}
	err := d.w.Walk(d.ctx, []byte("diagnostic/unique/"), func(row transfer.SpoolRow) error {
		key := string(row.Key)
		key = key[:strings.LastIndexByte(key, '/')]
		if key != group {
			if err := flush(); err != nil {
				return err
			}
			group = key
			count = 0
			if err := UnmarshalState(row.Value, &first); err != nil {
				return err
			}
		}
		count++
		// Each excess row is emitted as well, so details preserve every participant
		// after the first with no unbounded per-group array in the summary.
		if count > 1 {
			var ref DiagnosticFinding
			if err := UnmarshalState(row.Value, &ref); err != nil {
				return err
			}
			ref.Code = "duplicate_member." + strings.Split(group, "/")[2]
			ref.RelatedKeySHA256 = first.KeySHA256
			return d.emit(ref)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (d *diagnostician) indexGroups() error {
	// Original sender lookup allows only stale positions below a proved later
	// live sender sequence. Reduce the full ordered inventory, never an LRU.
	var sender string
	var maxSeq uint64
	flushSender := func() error {
		if sender == "" {
			return nil
		}
		return d.put("diagnostic/sender-max/"+sender, maxSeq)
	}
	err := d.w.Walk(d.ctx, []byte("diagnostic/sender/"), func(row transfer.SpoolRow) error {
		key := strings.TrimPrefix(string(row.Key), "diagnostic/sender/")
		key = key[:strings.LastIndexByte(key, '/')]
		if key != sender {
			if err := flushSender(); err != nil {
				return err
			}
			sender = key
		}
		return UnmarshalState(row.Value, &maxSeq)
	})
	if err != nil {
		return err
	}
	if err := flushSender(); err != nil {
		return err
	}
	if err := d.batch.flush(); err != nil {
		return err
	}
	var group string
	var actual *diagnosticIndexRow
	var first *diagnosticIndexRow
	var count uint64
	var collision bool
	flush := func() error {
		if collision {
			ref := first.Ref
			ref.Code = "index.expected_collision"
			ref.Count = count
			if err := d.emit(ref); err != nil {
				return err
			}
		}
		if actual == nil || count > 0 {
			return nil
		}
		a := actual
		if len(a.Entry.SenderKey) > 0 {
			seq, found, err := diagnosticGet[uint64](d.ctx, d.w, fmt.Sprintf("diagnostic/sender-max/%020d/%04d/%x", a.Ref.NodeID, a.Ref.Shard, a.Entry.SenderKey))
			if err != nil {
				return err
			}
			if found && seq > a.Entry.SenderSeq {
				return nil
			}
		}
		if a.Entry.AllowAbsentPrimary {
			_, found, err := d.w.Get(d.ctx, sourceRowKey(a.Ref.NodeID, Row{Shard: a.Ref.Shard, Key: a.Entry.PrimaryKey}))
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
		}
		ref := a.Ref
		ref.Code = "index.orphan_or_mismatch"
		return d.emit(ref)
	}
	err = d.w.Walk(d.ctx, []byte("diagnostic/index/"), func(row transfer.SpoolRow) error {
		parts := strings.Split(string(row.Key), "/")
		key := strings.Join(parts[:5], "/")
		if key != group {
			if err := flush(); err != nil {
				return err
			}
			group = key
			actual = nil
			first = nil
			count = 0
			collision = false
		}
		var item diagnosticIndexRow
		if err := UnmarshalState(row.Value, &item); err != nil {
			return err
		}
		if parts[5] == "a" {
			actual = &item
			return nil
		}
		count++
		if first == nil {
			first = &item
		} else if !bytes.Equal(first.Entry.Value, item.Entry.Value) {
			collision = true
		}
		if actual == nil {
			item.Ref.Code = "index.missing"
			return d.emit(item.Ref)
		}
		if !bytes.Equal(actual.Entry.Value, item.Entry.Value) {
			item.Ref.Code = "index.value_mismatch"
			item.Ref.RelatedKeySHA256 = actual.Ref.KeySHA256
			return d.emit(item.Ref)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (d *diagnostician) logs(prefix string) error {
	var group string
	var first, last, tail diagnosticLogRow
	var count uint64
	var hasTail bool
	flush := func() error {
		if group == "" {
			return nil
		}
		if !hasTail {
			ref := first.Ref
			ref.Code = "message.tail_missing"
			return d.emit(ref)
		}
		if count == 0 && tail.Seq > 0 {
			ref := tail.Ref
			ref.Code = "message.retained_prefix"
			ref.Count = tail.Seq
			return d.emit(ref)
		}
		if count > 0 && last.Seq != tail.Seq {
			ref := tail.Ref
			ref.Code = "message.tail_mismatch"
			ref.Count = last.Seq
			return d.emit(ref)
		}
		return nil
	}
	err := d.w.Walk(d.ctx, []byte(prefix+"log/"), func(row transfer.SpoolRow) error {
		suffix := strings.TrimPrefix(string(row.Key), prefix+"log/")
		key := strings.SplitN(suffix, "/", 2)[0]
		if key != group {
			if err := flush(); err != nil {
				return err
			}
			group = key
			count = 0
			hasTail = false
		}
		var item diagnosticLogRow
		if err := UnmarshalState(row.Value, &item); err != nil {
			return err
		}
		if item.Tail {
			if hasTail && item.Seq != tail.Seq {
				ref := item.Ref
				ref.Code = "message.conflicting_tails"
				ref.RelatedKeySHA256 = tail.Ref.KeySHA256
				return d.emit(ref)
			}
			tail = item
			hasTail = true
			return nil
		}
		if count == 0 {
			first = item
			if item.Seq != 1 {
				ref := item.Ref
				ref.Code = "message.retained_prefix"
				if item.Seq > 0 {
					ref.Count = item.Seq - 1
				}
				if err := d.emit(ref); err != nil {
					return err
				}
			}
		} else if item.Seq != last.Seq+1 {
			ref := item.Ref
			ref.Code = "message.sequence_gap"
			ref.RelatedKeySHA256 = last.Ref.KeySHA256
			if err := d.emit(ref); err != nil {
				return err
			}
		}
		last = item
		count++
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (d *diagnostician) events(prefix string) error {
	type event struct {
		Ref   DiagnosticFinding
		Facts BusinessFacts
	}
	if err := d.w.Walk(d.ctx, []byte(prefix+"event/state/"), func(row transfer.SpoolRow) error {
		var item event
		if err := UnmarshalState(row.Value, &item); err != nil {
			return err
		}
		state := item.Facts.EventState
		key := tuple(state.ChannelID, uint8(state.ChannelType), state.ClientMsgNo)
		var cursor event
		var count uint64
		err := d.w.Walk(d.ctx, []byte(prefix+"event/cursor/"+key+"/"), func(row transfer.SpoolRow) error {
			count++
			if count == 1 {
				return UnmarshalState(row.Value, &cursor)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count != 1 || cursor.Facts.EventCursor == nil {
			item.Ref.Code = "event.cursor_missing_or_ambiguous"
			return d.emit(item.Ref)
		}
		if err := meta.ValidateImportedMessageEvent(*state, *cursor.Facts.EventCursor); err != nil {
			item.Ref.Code = "event.native_projection"
			return d.emit(item.Ref)
		}
		return nil
	}); err != nil {
		return err
	}
	return d.w.Walk(d.ctx, []byte(prefix+"event/cursor/"), func(row transfer.SpoolRow) error {
		var item event
		if err := UnmarshalState(row.Value, &item); err != nil {
			return err
		}
		key := strings.TrimPrefix(string(row.Key), prefix+"event/cursor/")
		key = key[:strings.LastIndexByte(key, '/')]
		found := false
		if err := d.w.Walk(d.ctx, []byte(prefix+"event/state/"+key+"/"), func(transfer.SpoolRow) error { found = true; return nil }); err != nil {
			return err
		}
		if !found {
			item.Ref.Code = "event.projection_missing"
			return d.emit(item.Ref)
		}
		return nil
	})
}
