package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	"github.com/WuKongIM/WuKongIM/internal/app"
	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/pelletier/go-toml/v2"
)

// normalizedDocumentConfig reuses the owning runtime defaults without starting
// goroutines or changing the configuration passed to App.New.
func normalizedDocumentConfig(cfg app.Config) (app.Config, error) {
	cfg, err := app.NormalizeConfig(cfg)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Cluster = cfg.Cluster.WithDefaults()
	cfg.Gateway.Runtime = gateway.NormalizeRuntimeOptions(cfg.Gateway.Runtime)
	cfg.Gateway.Session = gateway.NormalizeSessionOptions(cfg.Gateway.Session)
	if cfg.Gateway.SendTimeout <= 0 {
		cfg.Gateway.SendTimeout = accessgateway.DefaultSendTimeout
	}
	channel := &cfg.Cluster.Channel
	if channel.StoreAppendWorkers == 0 {
		channel.StoreAppendWorkers = worker.DefaultStoreAppendWorkers
	}
	if channel.StoreApplyWorkers == 0 {
		channel.StoreApplyWorkers = worker.DefaultStoreApplyWorkers
	}
	if channel.StoreAppendBatchMaxWait == 0 {
		channel.StoreAppendBatchMaxWait = worker.DefaultStoreAppendBatchMaxWait
	}
	rc := reactor.NormalizeConfig(reactor.ReactorConfig{
		AppendBatchMaxRecords:         channel.AppendBatchMaxRecords,
		AppendBatchMaxWait:            channel.AppendBatchMaxWait,
		FollowerRecoveryProbeInterval: channel.FollowerRecoveryProbeInterval,
		FollowerRecoveryProbeJitter:   channel.FollowerRecoveryProbeJitter,
	})
	channel.AppendBatchMaxRecords = rc.AppendBatchMaxRecords
	channel.AppendBatchMaxWait = rc.AppendBatchMaxWait
	channel.FollowerRecoveryProbeInterval = rc.FollowerRecoveryProbeInterval
	channel.FollowerRecoveryProbeJitter = rc.FollowerRecoveryProbeJitter
	if channel.AppendBatchColdMaxWait == 0 {
		channel.AppendBatchColdMaxWait = channel.AppendBatchMaxWait
	}
	commit := messagedb.NormalizeCommitCoordinatorConfig(messagedb.CommitCoordinatorConfig{
		FlushWindow: cfg.Cluster.Storage.CommitFlushWindow,
	})
	cfg.Cluster.Storage.CommitFlushWindow = commit.FlushWindow
	return cfg, nil
}

// buildStartupDocument serializes only public fields after effective-value
// projection and redaction. Raw paths, object lists and secrets never enter DTOs.
func buildStartupDocument(values sourceValues, cfg app.Config) (management.NodeConfigDocument, error) {
	normalized, err := normalizedDocumentConfig(cfg)
	if err != nil {
		return management.NodeConfigDocument{}, err
	}
	effective := effectiveConfigValues(normalized)
	critical := effectiveCriticalSnapshotValues(values, cfg)
	document := management.NodeConfigDocument{
		NodeID: cfg.NodeID, GeneratedAt: time.Now().UTC(),
		Source: management.NodeConfigSnapshotSourceEffectiveStartup, RequiresRestart: true,
	}
	var lines []string
	section := ""
	for _, field := range schemaFields {
		value, ok := effective[field.EnvKey]
		if !ok {
			return management.NodeConfigDocument{}, fmt.Errorf("config document: missing projection for %s", field.TOMLPath)
		}
		table, key, ok := strings.Cut(field.TOMLPath, ".")
		if !ok {
			return management.NodeConfigDocument{}, fmt.Errorf("config document: invalid schema path")
		}
		// Public nested tables (for example webhook.before_send) use their full parent path.
		if index := strings.LastIndex(field.TOMLPath, "."); index >= 0 {
			table, key = field.TOMLPath[:index], field.TOMLPath[index+1:]
		}
		if table != section {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			document.Sections = append(document.Sections, management.NodeConfigDocumentSection{Path: table, Line: len(lines) + 1})
			lines = append(lines, "["+table+"]")
			section = table
		}
		help := schemaHelp[field.TOMLPath]
		if help.EN == "" || help.ZH == "" {
			return management.NodeConfigDocument{}, fmt.Errorf("config document: missing help for %s", field.TOMLPath)
		}
		source := documentValueSource(field, values, value)
		if item, ok := critical[field.EnvKey]; ok {
			source = item.source
		}
		hidden := field.Sensitive || isPathLikeField(field.TOMLPath) || field.Kind == kindObjectList
		document.Fields = append(document.Fields, management.NodeConfigDocumentField{
			Path: field.TOMLPath, EnvKey: field.EnvKey, Label: field.Label,
			Description: help.EN, DescriptionZH: help.ZH, Source: source,
			Line: len(lines) + 1, Redacted: hidden,
		})
		if hidden {
			lines = append(lines, "# "+key+": hidden in this redacted startup snapshot")
			continue
		}
		encoded, err := encodeDocumentValue(key, field.Kind, value)
		if err != nil {
			return management.NodeConfigDocument{}, fmt.Errorf("config document: cannot encode %s", field.TOMLPath)
		}
		lines = append(lines, strings.Split(strings.TrimSuffix(encoded, "\n"), "\n")...)
	}
	document.TOML = strings.Join(lines, "\n") + "\n"
	if err := document.Validate(cfg.NodeID); err != nil {
		return management.NodeConfigDocument{}, err
	}
	body, err := json.Marshal(document)
	if err != nil || len(body) > management.MaxNodeConfigDocumentBytes {
		return management.NodeConfigDocument{}, management.ErrNodeConfigUnavailable
	}
	return document, nil
}

func encodeDocumentValue(key string, kind fieldKind, value any) (string, error) {
	if duration, ok := value.(time.Duration); ok {
		value = duration.String()
	}
	if kind == kindStringList && reflect.ValueOf(value).IsNil() {
		value = []string{}
	}
	body, err := toml.Marshal(map[string]any{key: value})
	return string(body), err
}

// documentValueSource compares typed inputs so equivalent spelling (e.g. 1000ms
// and 1s) does not falsely claim runtime derivation. It never retains input text.
func documentValueSource(field fieldSpec, values sourceValues, value any) string {
	raw, configured := values.values[field.EnvKey]
	if !configured {
		switch field.TOMLPath {
		case "cluster.id", "cluster.nodes", "channel_append.shard_count",
			"channel_append.advance_pool_size", "channel_append.effect_pool_size":
			return management.NodeConfigValueSourceDerived
		}
		return management.NodeConfigValueSourceDefault
	}
	equal := false
	switch field.Kind {
	case kindString:
		equal = raw == fmt.Sprint(value)
	case kindDuration:
		input, err := time.ParseDuration(raw)
		equal = err == nil && input == value
	case kindBool:
		input, err := parseBool(field.EnvKey, raw)
		equal = err == nil && input == value
	case kindStringList:
		input, err := parseStringList(field.EnvKey, raw)
		equal = err == nil && reflect.DeepEqual(input, value)
	case kindObjectList:
		// Lists are hidden; source denotes the selected input, never a reconstructed payload.
		equal = true
	case kindFloat:
		input, err := strconv.ParseFloat(raw, 64)
		equal = err == nil && input == value
	case kindUint16, kindUint32, kindUint64:
		input, err := strconv.ParseUint(raw, 10, 64)
		equal = err == nil && strconv.FormatUint(input, 10) == fmt.Sprint(value)
	default:
		input, err := strconv.ParseInt(raw, 10, 64)
		equal = err == nil && strconv.FormatInt(input, 10) == fmt.Sprint(value)
	}
	if !equal {
		return management.NodeConfigValueSourceDerived
	}
	return configuredValueSource(values.sources[field.EnvKey])
}
