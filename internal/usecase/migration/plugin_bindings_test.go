package migration

import (
	"strings"
	"testing"
)

func TestPluginBindingNativeLimitsAndOriginalTimestampOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		p     SourcePluginBinding
		valid bool
	}{
		{"zero-timestamps", SourcePluginBinding{UID: "u", PluginNo: "p"}, true},
		{"negative-timestamps", SourcePluginBinding{UID: "u", PluginNo: "p", CreatedAtNS: -1000001, UpdatedAtNS: -1}, true},
		{"opaque-key", SourcePluginBinding{UID: string([]byte{0xff, 0}), PluginNo: "p"}, true},
		{"empty-uid", SourcePluginBinding{PluginNo: "p"}, false},
		{"empty-plugin", SourcePluginBinding{UID: "u"}, false},
		{"long-uid", SourcePluginBinding{UID: strings.Repeat("u", 65536), PluginNo: "p"}, false},
		{"long-plugin", SourcePluginBinding{UID: "u", PluginNo: strings.Repeat("p", 65536)}, false},
		{"native-limit", SourcePluginBinding{UID: strings.Repeat("u", 65535), PluginNo: "p"}, true},
		{"sub-millisecond-backwards", SourcePluginBinding{UID: "u", PluginNo: "p", CreatedAtNS: 1000002, UpdatedAtNS: 1000001}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginBinding(&tc.p)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
