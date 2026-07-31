package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/server"
	"gopkg.in/yaml.v3"
)

const (
	upstreamRoutesDocumentVersion       = 1
	defaultUpstreamRoutesFileMaxSize    = int64(1 << 20)
	maxUpstreamRoutesFileMaxSize        = int64(16 << 20)
	maxUpstreamRoutesDocumentRoutes     = 256
	maxUpstreamRouteNameLength          = 128
	maxUpstreamReloadAcceptedRanges     = 64
	maxUpstreamReloadAcceptedMsgIDs     = uint64(20000)
	defaultUpstreamReloadDrainTimeout   = 30 * time.Second
	minUpstreamReloadDrainTimeout       = time.Second
	maxUpstreamReloadDrainTimeout       = 10 * time.Minute
	maxUpstreamReloadAcceptedRangeWidth = uint32(10000)
)

type upstreamRoutesDocument struct {
	Version int                   `yaml:"version"`
	Routes  []UpstreamRouteConfig `yaml:"routes"`
}

type resolvedUpstreamRoutes struct {
	routes     []UpstreamRouteConfig
	fileConfig server.UpstreamRoutesFileConfig
}

func (c *File) resolveUpstreamRoutes() (resolvedUpstreamRoutes, error) {
	if c == nil {
		return resolvedUpstreamRoutes{}, fmt.Errorf("config: file is nil")
	}

	if len(c.Upstream.Routes) > 0 && c.Upstream.RoutesFile != nil {
		return resolvedUpstreamRoutes{}, fmt.Errorf("config: upstream.routes and upstream.routes_file are mutually exclusive")
	}
	if c.Upstream.RoutesFile == nil {
		return resolvedUpstreamRoutes{
			routes: append([]UpstreamRouteConfig(nil), c.Upstream.Routes...),
		}, nil
	}

	fileConfig, err := c.toServerUpstreamRoutesFileConfig()
	if err != nil {
		return resolvedUpstreamRoutes{}, err
	}
	document, err := loadUpstreamRoutesDocument(fileConfig.Path, fileConfig.MaxSizeBytes)
	if err != nil {
		return resolvedUpstreamRoutes{}, err
	}

	return resolvedUpstreamRoutes{
		routes:     document.Routes,
		fileConfig: fileConfig,
	}, nil
}

func (c *File) toServerUpstreamRoutesFileConfig() (server.UpstreamRoutesFileConfig, error) {
	if c.Upstream.RoutesFile == nil {
		return server.UpstreamRoutesFileConfig{}, fmt.Errorf("config: upstream.routes_file is not configured")
	}
	config := *c.Upstream.RoutesFile
	rawPath := strings.TrimSpace(config.Path)
	if rawPath == "" {
		return server.UpstreamRoutesFileConfig{}, fmt.Errorf("config: upstream.routes_file.path is required")
	}

	maxSize := config.MaxSizeBytes
	if maxSize == 0 {
		maxSize = defaultUpstreamRoutesFileMaxSize
	}
	if maxSize < 1 || maxSize > maxUpstreamRoutesFileMaxSize {
		return server.UpstreamRoutesFileConfig{}, fmt.Errorf(
			"config: upstream.routes_file.max_size_bytes must be between 1 and %d",
			maxUpstreamRoutesFileMaxSize,
		)
	}

	reload, err := toServerUpstreamRouteReloadConfig(config.Reload)
	if err != nil {
		return server.UpstreamRoutesFileConfig{}, err
	}

	resolvedPath, err := c.resolveRelativePath(rawPath)
	if err != nil {
		return server.UpstreamRoutesFileConfig{}, fmt.Errorf("config: resolve upstream routes file %q: %w", rawPath, err)
	}

	return server.UpstreamRoutesFileConfig{
		Path:         resolvedPath,
		MaxSizeBytes: maxSize,
		Reload:       reload,
	}, nil
}

func (c *File) resolveRelativePath(rawPath string) (string, error) {
	if filepath.IsAbs(rawPath) {
		return filepath.Clean(rawPath), nil
	}
	if c.sourcePath != "" {
		return filepath.Join(filepath.Dir(c.sourcePath), rawPath), nil
	}
	return filepath.Abs(rawPath)
}

func toServerUpstreamRouteReloadConfig(config UpstreamRouteReloadConfig) (server.UpstreamRouteReloadConfig, error) {
	if !config.Enabled {
		if strings.TrimSpace(config.DrainTimeout) != "" || len(config.AcceptedMsgIDRanges) > 0 {
			return server.UpstreamRouteReloadConfig{}, fmt.Errorf(
				"config: upstream.routes_file.reload settings require enabled: true",
			)
		}
		return server.UpstreamRouteReloadConfig{}, nil
	}
	if len(config.AcceptedMsgIDRanges) == 0 {
		return server.UpstreamRouteReloadConfig{}, fmt.Errorf(
			"config: upstream.routes_file.reload.accepted_msg_id_ranges is required when reload is enabled",
		)
	}

	drainTimeout := defaultUpstreamReloadDrainTimeout
	if strings.TrimSpace(config.DrainTimeout) != "" {
		parsed, err := time.ParseDuration(config.DrainTimeout)
		if err != nil {
			return server.UpstreamRouteReloadConfig{}, fmt.Errorf(
				"config: upstream.routes_file.reload.drain_timeout: %w",
				err,
			)
		}
		drainTimeout = parsed
	}
	if drainTimeout < minUpstreamReloadDrainTimeout || drainTimeout > maxUpstreamReloadDrainTimeout {
		return server.UpstreamRouteReloadConfig{}, fmt.Errorf(
			"config: upstream.routes_file.reload.drain_timeout must be between %s and %s",
			minUpstreamReloadDrainTimeout,
			maxUpstreamReloadDrainTimeout,
		)
	}

	ranges, err := normalizeUpstreamReloadAcceptedRanges(config.AcceptedMsgIDRanges)
	if err != nil {
		return server.UpstreamRouteReloadConfig{}, err
	}
	return server.UpstreamRouteReloadConfig{
		Enabled:             true,
		DrainTimeout:        drainTimeout,
		AcceptedMsgIDRanges: ranges,
	}, nil
}

func normalizeUpstreamReloadAcceptedRanges(config []MsgIDRangeConfig) ([]server.MsgIDRange, error) {
	if len(config) > maxUpstreamReloadAcceptedRanges {
		return nil, fmt.Errorf(
			"config: upstream.routes_file.reload.accepted_msg_id_ranges exceeds %d ranges",
			maxUpstreamReloadAcceptedRanges,
		)
	}

	ranges := make([]server.MsgIDRange, 0, len(config))
	var total uint64
	for index, candidate := range config {
		maxMsgID := candidate.Max
		if maxMsgID == 0 {
			maxMsgID = candidate.Min
		}
		if candidate.Min == 0 || maxMsgID < candidate.Min {
			return nil, fmt.Errorf(
				"config: upstream.routes_file.reload accepted range #%d is invalid: %d-%d",
				index+1,
				candidate.Min,
				candidate.Max,
			)
		}
		if maxMsgID-candidate.Min > maxUpstreamReloadAcceptedRangeWidth {
			return nil, fmt.Errorf(
				"config: upstream.routes_file.reload accepted range #%d is too large: %d-%d",
				index+1,
				candidate.Min,
				maxMsgID,
			)
		}
		for _, reserved := range reservedMsgIDs() {
			if candidate.Min <= reserved && reserved <= maxMsgID {
				return nil, fmt.Errorf(
					"config: upstream.routes_file.reload accepted range #%d uses reserved msg_id %d",
					index+1,
					reserved,
				)
			}
		}

		total += uint64(maxMsgID-candidate.Min) + 1
		if total > maxUpstreamReloadAcceptedMsgIDs {
			return nil, fmt.Errorf(
				"config: upstream.routes_file.reload accepted ranges exceed %d msg IDs",
				maxUpstreamReloadAcceptedMsgIDs,
			)
		}
		ranges = append(ranges, server.MsgIDRange{Min: candidate.Min, Max: maxMsgID})
	}

	sort.Slice(ranges, func(left, right int) bool {
		if ranges[left].Min == ranges[right].Min {
			return ranges[left].Max < ranges[right].Max
		}
		return ranges[left].Min < ranges[right].Min
	})
	for index := 1; index < len(ranges); index++ {
		previous := ranges[index-1]
		current := ranges[index]
		if current.Min <= previous.Max {
			return nil, fmt.Errorf(
				"config: upstream.routes_file.reload accepted range %d-%d overlaps %d-%d",
				current.Min,
				current.Max,
				previous.Min,
				previous.Max,
			)
		}
	}
	return ranges, nil
}

func loadUpstreamRoutesDocument(path string, maxSize int64) (upstreamRoutesDocument, error) {
	file, err := os.Open(path)
	if err != nil {
		return upstreamRoutesDocument{}, fmt.Errorf("config: read upstream routes %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return upstreamRoutesDocument{}, fmt.Errorf("config: read upstream routes %s: %w", path, err)
	}
	if int64(len(data)) > maxSize {
		return upstreamRoutesDocument{}, fmt.Errorf(
			"config: upstream routes file %s exceeds max_size_bytes %d",
			path,
			maxSize,
		)
	}
	data, err = expandEnvPlaceholders(data)
	if err != nil {
		return upstreamRoutesDocument{}, fmt.Errorf("config: expand env upstream routes %s: %w", path, err)
	}

	var document upstreamRoutesDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return upstreamRoutesDocument{}, fmt.Errorf("config: parse upstream routes %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return upstreamRoutesDocument{}, fmt.Errorf("config: parse upstream routes %s: multiple YAML documents are not allowed", path)
		}
		return upstreamRoutesDocument{}, fmt.Errorf("config: parse upstream routes %s: %w", path, err)
	}

	if document.Version != upstreamRoutesDocumentVersion {
		return upstreamRoutesDocument{}, fmt.Errorf(
			"config: upstream routes file %s version must be %d",
			path,
			upstreamRoutesDocumentVersion,
		)
	}
	if err := validateUpstreamRoutesDocumentLimits(document.Routes); err != nil {
		return upstreamRoutesDocument{}, err
	}
	return document, nil
}

func validateUpstreamRoutesDocumentLimits(routes []UpstreamRouteConfig) error {
	if len(routes) > maxUpstreamRoutesDocumentRoutes {
		return fmt.Errorf(
			"config: upstream routes file exceeds %d routes",
			maxUpstreamRoutesDocumentRoutes,
		)
	}

	names := make(map[string]int, len(routes))
	for index, route := range routes {
		name := strings.TrimSpace(route.Name)
		if name == "" {
			return fmt.Errorf("config: upstream routes file route #%d requires name", index+1)
		}
		if name != route.Name {
			return fmt.Errorf("config: upstream routes file route #%d name must not have surrounding whitespace", index+1)
		}
		if len(name) > maxUpstreamRouteNameLength {
			return fmt.Errorf(
				"config: upstream routes file route %q name exceeds %d bytes",
				name,
				maxUpstreamRouteNameLength,
			)
		}
		if previous, exists := names[name]; exists {
			return fmt.Errorf(
				"config: upstream routes file route %q duplicates route #%d",
				name,
				previous+1,
			)
		}
		names[name] = index
	}
	return nil
}

func validateRoutesInsideReloadRanges(
	routes []UpstreamRouteConfig,
	config server.UpstreamRoutesFileConfig,
	collector *validationCollector,
) {
	if !config.Reload.Enabled {
		return
	}

	for index, route := range routes {
		if !upstreamRouteEnabled(route) || validateMsgIDRange(route) != nil {
			continue
		}
		maxMsgID := route.MsgIDMax
		if maxMsgID == 0 {
			maxMsgID = route.MsgIDMin
		}
		accepted := false
		for _, allowed := range config.Reload.AcceptedMsgIDRanges {
			if allowed.Min <= route.MsgIDMin && maxMsgID <= allowed.Max {
				accepted = true
				break
			}
		}
		if !accepted {
			collector.addProblem(
				"enabled upstream route %s range %d-%d is outside reload accepted_msg_id_ranges",
				upstreamRouteLabel(route, index),
				route.MsgIDMin,
				maxMsgID,
			)
		}
	}
}
