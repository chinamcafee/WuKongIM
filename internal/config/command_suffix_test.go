package config

import (
	"github.com/WuKongIM/WuKongIM/internal/app"
	"path/filepath"
	"testing"
)

func TestCommandSuffixTOMLAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	base := "[node]\nid = 1\ndata_dir = \"" + dir + "/data\"\n[cluster]\nlisten_addr = \"127.0.0.1:7001\"\n"
	for _, tc := range []struct{ name, file, env, want string }{
		{name: "default", want: "____cmd"},
		{name: "toml", file: "__commands", want: "__commands"},
		{name: "environment overrides file", file: "__commands", env: "__events", want: "__events"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, path, base+"[message]\ncmd_channel_suffix = \""+tc.file+"\"\n")
			env := cleanEnv()
			if tc.env != "" {
				env = append(env, "WK_MESSAGE_CMD_CHANNEL_SUFFIX="+tc.env)
			}
			cfg, err := Load(Options{Args: []string{"-config", path}, Environ: env})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Message.CMDChannelSuffix != tc.want {
				t.Fatalf("suffix=%q want=%q", cfg.Message.CMDChannelSuffix, tc.want)
			}
		})
	}
	for _, invalid := range []string{"@cmd", "#cmd", "&cmd", "has space", "命令"} {
		t.Run("invalid="+invalid, func(t *testing.T) {
			writeFile(t, path, base+"[message]\ncmd_channel_suffix = \""+invalid+"\"\n")
			cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.NormalizeConfig(cfg); err == nil {
				t.Fatal("accepted invalid command suffix")
			}
		})
	}
}
