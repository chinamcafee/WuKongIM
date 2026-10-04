package migrationv2

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"unicode/utf8"
)

type pendingConversation struct {
	ChannelID      string            `json:"channel_id"`
	ChannelType    uint8             `json:"channel_type"`
	UserReadSeqs   map[string]uint64 `json:"user_read_seqs"`
	TagKey         string            `json:"tag_key"`
	LastMessageSeq uint64            `json:"last_msg_seq"`
}

// scanConversationTail exposes recoverable intents separately from stored
// conversations. The migration use case resolves absence at the authoritative
// UID Slot, mirroring original AddConversationsIfNotExist semantics.
func scanConversationTail(ctx context.Context, root string, maxBytes int, visit func(Row) error) (err error) {
	f, err := os.Open(filepath.Join(root, "conversationv2", "conversations.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	budget := &jsonBudgetReader{ctx: ctx, r: f, remaining: int64(maxBytes)}
	dec := json.NewDecoder(budget)
	token, err := dec.Token()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if token == nil {
		return requireJSONEnd(dec)
	}
	if token != json.Delim('[') {
		return errors.New("v2 conversation recovery file must contain an array")
	}
	for {
		// Decoder read-ahead is charged to the next object, keeping a single large
		// group's map and JSON buffer bounded even in a many-gigabyte tail file.
		buffered := budget.read - dec.InputOffset()
		budget.remaining = int64(maxBytes) - buffered
		if budget.remaining < 0 {
			return errors.New("v2 conversation recovery record exceeds byte limit")
		}
		if !dec.More() {
			break
		}
		start := dec.InputOffset()
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("v2 conversation recovery record: %w", err)
		}
		if dec.InputOffset()-start > int64(maxBytes) || !utf8.Valid(raw) {
			return errors.New("invalid or oversized v2 conversation recovery record")
		}
		var update pendingConversation
		if err := decodeStrictJSON(raw, &update); err != nil {
			return err
		}
		if update.ChannelID == "" || update.ChannelType == 0 || update.LastMessageSeq == 0 {
			return errors.New("invalid v2 conversation recovery channel")
		}
		uids := make([]string, 0, len(update.UserReadSeqs))
		for uid := range update.UserReadSeqs {
			uids = append(uids, uid)
		}
		sort.Strings(uids)
		for _, uid := range uids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if uid == "" {
				// SourceCommit's storeConversations skips an empty UID before
				// constructing any durable row. Preserve the exact cache object
				// as provenance instead of inventing a user or rejecting recovery.
				if err := visit(ignoredConversationRow(update, raw)); err != nil {
					return err
				}
				continue
			}
			read := update.UserReadSeqs[uid]
			if read > update.LastMessageSeq {
				return errors.New("invalid v2 conversation recovery read position")
			}
			// This is a namespaced intent key, not a fabricated original row ID.
			key, _ := json.Marshal([]string{update.ChannelID, strconv.Itoa(int(update.ChannelType)), uid})
			key = append([]byte{0xff, 0x01}, key...)
			fields := map[string][]byte{"Uid": []byte(uid), "ChannelId": []byte(update.ChannelID), "ChannelType": {update.ChannelType}, "ReadedToMsgSeq": make([]byte, 8), "LastMsgSeq": make([]byte, 8)}
			binary.BigEndian.PutUint64(fields["ReadedToMsgSeq"], read)
			binary.BigEndian.PutUint64(fields["LastMsgSeq"], update.LastMessageSeq)
			if err := visit(Row{Table: "PendingConversation", Kind: Primary, Key: key, Fields: fields}); err != nil {
				return err
			}
		}
	}
	if token, err := dec.Token(); err != nil || token != json.Delim(']') {
		return errors.New("incomplete v2 conversation recovery array")
	}
	budget.remaining = int64(maxBytes)
	return requireJSONEnd(dec)
}

func ignoredConversationRow(update pendingConversation, raw []byte) Row {
	key, _ := json.Marshal([]string{update.ChannelID, strconv.Itoa(int(update.ChannelType)), ""})
	return Row{Table: "IgnoredConversation", Kind: Other, Key: append([]byte{0xff, 0x02}, key...), Value: bytes.Clone(raw)}
}

// validateIgnoredConversation checks archived provenance again before selection;
// only the empty-UID entry ignored by the original recovery code has this kind.
func validateIgnoredConversation(row Row) error {
	var update pendingConversation
	if !utf8.Valid(row.Value) || decodeStrictJSON(row.Value, &update) != nil {
		return errors.New("invalid ignored conversation recovery object")
	}
	_, emptyUID := update.UserReadSeqs[""]
	if !emptyUID || update.ChannelID == "" || update.ChannelType == 0 || update.LastMessageSeq == 0 || row.Kind != Other || row.Shard != 0 || row.Owner != 0 || row.ID != 0 || len(row.Fields) != 0 || !bytes.Equal(row.Key, ignoredConversationRow(update, nil).Key) {
		return errors.New("invalid ignored conversation recovery identity")
	}
	return nil
}

// jsonBudgetReader limits the next decode including decoder read-ahead. A hard
// budget error is not EOF, so a truncated document cannot appear complete.
type jsonBudgetReader struct {
	ctx             context.Context
	r               io.Reader
	remaining, read int64
}

func (r *jsonBudgetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining <= 0 {
		return 0, errors.New("v2 JSON record exceeds byte limit")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	r.read += int64(n)
	return n, err
}

func decodeStrictJSON(data []byte, value any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	return requireJSONEnd(dec)
}
