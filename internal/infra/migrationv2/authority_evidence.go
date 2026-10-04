package migrationv2

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"slices"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	wkproto "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
)

func evidenceSHA(v []byte) string { h := sha256.Sum256(v); return hex.EncodeToString(h[:]) }

// InspectChannelConfig decodes roles for diagnosis and source-proof rebuilding.
// It does not weaken the default decodeChannelAuthority rejection behavior.
func (Reader) InspectChannelConfig(row Row) (migration.ChannelConfigEvidence, error) {
	return inspectChannelConfig(row, false)
}

func inspectChannelConfig(row Row, allowEmpty bool) (c migration.ChannelConfigEvidence, err error) {
	if row.Table != "ChannelClusterConfig" || row.Kind != Primary {
		return c, errors.New("expected channel config primary")
	}
	id, err := Identify(row)
	if allowEmpty && emptyChannelPrimary(row) {
		id, err = RecordIdentity{}, nil
	}
	if err != nil {
		return c, err
	}
	c.Owner, c.IdentitySHA256 = row.ID, evidenceSHA([]byte(migration.IdentityKey(id.Channel.ID, id.Channel.Type)))
	c.RoutingHash = crc32.ChecksumIEEE([]byte(id.Channel.ID))
	for name, out := range map[string]*uint64{"LeaderId": &c.Leader, "ConfVersion": &c.Version, "MigrateFrom": &c.MigrateFrom, "MigrateTo": &c.MigrateTo} {
		if *out, err = scalar64(row, name); err != nil {
			return c, err
		}
	}
	if len(row.Fields["Term"]) != 4 || len(row.Fields["ReplicaMaxCount"]) != 2 || len(row.Fields["Status"]) != 1 {
		return c, errors.New("incomplete channel configuration")
	}
	c.Term, c.ReplicaMax, c.Status = binary.BigEndian.Uint32(row.Fields["Term"]), binary.BigEndian.Uint16(row.Fields["ReplicaMaxCount"]), row.Fields["Status"][0]
	for name, out := range map[string]*[]uint64{"Replicas": &c.Replicas, "Learners": &c.Learners} {
		v := row.Fields[name]
		if len(v)%8 != 0 || len(v) > 8192 {
			return c, errors.New("invalid config members")
		}
		for len(v) > 0 {
			n := binary.BigEndian.Uint64(v)
			v = v[8:]
			if n == 0 || slices.Contains(*out, n) {
				return c, errors.New("duplicate or zero config member")
			}
			*out = append(*out, n)
		}
	}
	if c.Term == 0 || c.ReplicaMax == 0 || !slices.Contains(c.Replicas, c.Leader) {
		return c, errors.New("invalid config leader or term")
	}
	for _, n := range c.Learners {
		if slices.Contains(c.Replicas, n) {
			return c, errors.New("learner also in formal replicas")
		}
	}
	data, err := json.Marshal(row.Fields)
	if err != nil {
		return c, err
	}
	c.SHA256 = evidenceSHA(data)
	stable := cloneFields(row.Fields)
	for _, name := range []string{"ConfVersion", "MigrateFrom", "MigrateTo"} {
		delete(stable, name)
	}
	data, err = json.Marshal(stable)
	if err != nil {
		return c, err
	}
	c.NonMigrationSHA256 = evidenceSHA(data)
	return c, nil
}

func (Reader) InspectMessage(row Row, shards int) (m migration.MessageEvidence, err error) {
	msg, term, err := DecodeMessage(row)
	if err != nil {
		return m, err
	}
	if shards < 1 || row.Shard != int(row.Owner%uint64(shards)) {
		return m, errors.New("message in wrong original shard")
	}
	data, err := json.Marshal(row.Fields)
	if err != nil {
		return m, err
	}
	return migration.MessageEvidence{ID: msg.MessageID, Sequence: msg.MessageSeq, Term: term, SHA256: evidenceSHA(data)}, nil
}

func (Reader) ReadAuthorityNode(ctx context.Context, opts NodeOptions, rows func(Row) error, files func(SourceFile) error, logs func(migration.ChannelConfigLog) error) (NodeSnapshot, error) {
	return (Reader{}).ReadAuthorityCommands(ctx, opts, rows, files, func(command migration.RawConfigCommand) error {
		return logs((Reader{}).DecodeAuthorityCommand(command))
	})
}

func (Reader) ReadAuthorityCommands(ctx context.Context, opts NodeOptions, rows func(Row) error, files func(SourceFile) error, commands func(migration.RawConfigCommand) error) (NodeSnapshot, error) {
	return readStoppedNode(ctx, opts, rows, files, func(slot uint32, index uint64, term uint32, data []byte) error {
		if len(data) >= 4 {
			cmd := binary.BigEndian.Uint16(data[2:4])
			if cmd != 28 && cmd != 29 {
				return nil
			}
		}
		return commands(migration.RawConfigCommand{Slot: slot, Index: index, Term: term, Data: append([]byte(nil), data...)})
	})
}

// DecodeAuthorityCommand always reports malformed or unexpected captured input.
// Archive rebuilding never trusts previously decoded diagnostic fields.
func (Reader) DecodeAuthorityCommand(command migration.RawConfigCommand) migration.ChannelConfigLog {
	e, relevant, err := decodeConfigLog(command.Slot, command.Index, command.Term, command.Data)
	if !relevant && err == nil {
		err = errors.New("unexpected non-config command in authority capture")
	}
	if err != nil {
		e.Slot, e.Index, e.Term, e.CommandSHA256 = command.Slot, command.Index, command.Term, evidenceSHA(command.Data)
		e.DecodeErrorSHA256 = evidenceSHA([]byte(err.Error()))
	}
	return e
}

// decodeConfigLog follows original CMD framing and config version 1. Only
// commands 28/29 affect channel ownership. Other business commands stay opaque.
func decodeConfigLog(slot uint32, index uint64, term uint32, data []byte) (migration.ChannelConfigLog, bool, error) {
	return decodeConfigLogWithEmpty(slot, index, term, data, false)
}

func decodeConfigLogWithEmpty(slot uint32, index uint64, term uint32, data []byte, allowEmpty bool) (out migration.ChannelConfigLog, relevant bool, err error) {
	if len(data) < 4 {
		return out, false, errors.New("truncated Slot command")
	}
	cmd := binary.BigEndian.Uint16(data[2:4])
	if cmd != 28 && cmd != 29 {
		return out, false, nil
	}
	if cmd == 29 {
		return out, true, errors.New("original config delete command has no implemented apply format")
	}
	if binary.BigEndian.Uint16(data) != 1 {
		return out, true, errors.New("unsupported channel config command version")
	}
	out.Slot, out.Index, out.Term, out.Deleted, out.CommandSHA256 = slot, index, term, cmd == 29, evidenceSHA(data)
	d := wkproto.NewDecoder(data[4:])
	channelID, err := d.String()
	if err != nil {
		return out, true, err
	}
	channelType, err := d.Uint8()
	if err != nil {
		return out, true, err
	}
	if channelID == "" && !(allowEmpty && channelType == 0) {
		return out, true, errors.New("invalid config log identity")
	}
	out.Config.Owner = channelHash(channelID, channelType)
	out.Config.IdentitySHA256 = evidenceSHA([]byte(migration.IdentityKey(channelID, channelType)))
	out.Config.RoutingHash = crc32.ChecksumIEEE([]byte(channelID))
	if out.Deleted {
		rest, err := d.BinaryAll()
		if err != nil || len(rest) != 0 {
			return out, true, errors.New("trailing config delete data")
		}
		return out, true, nil
	}
	version, err := d.Uint16()
	if err != nil || version != 1 {
		return out, true, errors.New("unsupported channel config log format")
	}
	id, err := d.String()
	if err != nil {
		return out, true, err
	}
	typ, err := d.Uint8()
	if err != nil {
		return out, true, err
	}
	if id != channelID || typ != channelType {
		return out, true, errors.New("config log identity disagreement")
	}
	f := map[string][]byte{"ChannelId": []byte(id), "ChannelType": {typ}, "Version": {0, 1}}
	max, err := d.Uint16()
	if err != nil {
		return out, true, err
	}
	f["ReplicaMaxCount"] = []byte{byte(max >> 8), byte(max)}
	for _, name := range []string{"Replicas", "Learners"} {
		n, err := d.Uint16()
		if err != nil || n > 1024 {
			return out, true, errors.New("invalid config log member count")
		}
		f[name] = []byte{}
		for i := uint16(0); i < n; i++ {
			v, err := d.Uint64()
			if err != nil {
				return out, true, err
			}
			f[name] = append(f[name], uint64Bytes(v)...)
		}
	}
	leader, err := d.Uint64()
	if err != nil {
		return out, true, err
	}
	f["LeaderId"] = uint64Bytes(leader)
	t, err := d.Uint32()
	if err != nil {
		return out, true, err
	}
	f["Term"] = []byte{byte(t >> 24), byte(t >> 16), byte(t >> 8), byte(t)}
	for _, name := range []string{"MigrateFrom", "MigrateTo"} {
		n, err := d.Uint64()
		if err != nil {
			return out, true, err
		}
		f[name] = uint64Bytes(n)
	}
	status, err := d.Uint8()
	if err != nil {
		return out, true, err
	}
	f["Status"] = []byte{status}
	if out.EncodedVersion, err = d.Uint64(); err != nil {
		return out, true, err
	}
	f["ConfVersion"] = uint64Bytes(index)
	for _, name := range []string{"CreatedAt", "UpdatedAt"} {
		n, err := d.Uint64()
		if err != nil {
			return out, true, err
		}
		if n != 0 {
			f[name] = uint64Bytes(n)
		}
	}
	rest, err := d.BinaryAll()
	if err != nil || len(rest) != 0 {
		return out, true, errors.New("trailing config log data")
	}
	f["ConfVersion"] = uint64Bytes(out.EncodedVersion)
	encoded, err := json.Marshal(f)
	if err != nil {
		return out, true, err
	}
	out.EncodedConfigSHA256 = evidenceSHA(encoded)
	f["ConfVersion"] = uint64Bytes(index)
	key := make([]byte, 12)
	key[0], key[1], key[2] = 0x0b, 1, 1
	binary.BigEndian.PutUint64(key[4:], out.Config.Owner)
	out.Config, err = inspectChannelConfig(Row{Table: "ChannelClusterConfig", Kind: Primary, Key: key, ID: out.Config.Owner, Fields: f}, allowEmpty)
	return out, true, err
}
