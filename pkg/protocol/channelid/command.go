package channelid

import "strings"

// CommandChannelSuffix is the default suffix retained for legacy deployments.
const CommandChannelSuffix = "____cmd"

// CommandCodec encodes command channel IDs using one immutable deployment setting.
// The zero value uses CommandChannelSuffix. All cluster nodes must use the same suffix.
type CommandCodec struct {
	// Suffix is reserved for internal command channels; empty selects the default.
	Suffix string
}

func (c CommandCodec) suffix() string {
	if c.Suffix == "" {
		return CommandChannelSuffix
	}
	return c.Suffix
}

// IsCommandChannel reports whether an ID uses the configured command suffix.
func (c CommandCodec) IsCommandChannel(id string) bool { return strings.HasSuffix(id, c.suffix()) }

// ToCommandChannel appends the configured suffix once to a canonical source ID.
func (c CommandCodec) ToCommandChannel(id string) string {
	if c.IsCommandChannel(id) {
		return id
	}
	return id + c.suffix()
}

// FromCommandChannel removes the configured suffix before source-channel parsing.
func (c CommandCodec) FromCommandChannel(id string) (string, bool) {
	if !c.IsCommandChannel(id) {
		return id, false
	}
	return strings.TrimSuffix(id, c.suffix()), true
}

// IsCommandChannel reports whether an ID uses the default command suffix.
func IsCommandChannel(id string) bool { return (CommandCodec{}).IsCommandChannel(id) }

// ToCommandChannel applies the default command suffix.
func ToCommandChannel(id string) string { return (CommandCodec{}).ToCommandChannel(id) }

// FromCommandChannel removes the default command suffix.
func FromCommandChannel(id string) (string, bool) { return (CommandCodec{}).FromCommandChannel(id) }
