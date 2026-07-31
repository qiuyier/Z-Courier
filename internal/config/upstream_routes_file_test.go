package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadServerConfigFromUpstreamRoutesFile(t *testing.T) {
	t.Setenv("ZCOURIER_TEST_ROUTE_TOKEN", "route-token")

	directory := t.TempDir()
	routesPath := filepath.Join(directory, "upstream-routes.yaml")
	writeTestFile(t, routesPath, `
version: 1
routes:
  - name: events-nsq
    msg_id_min: 2000
    msg_id_max: 2999
    target:
      type: nsq
      nsqd_addrs:
        - 127.0.0.1:4150
      topic: message_events
  - name: business-http
    msg_id_min: 1001
    msg_id_max: 1999
    target:
      type: http
      url: http://backend.local/gateway/upstream
      token: ${ZCOURIER_TEST_ROUTE_TOKEN}
      timeout: 3s
`)
	configPath := filepath.Join(directory, "z-courier.yaml")
	writeTestFile(t, configPath, `
upstream:
  routes_file:
    path: upstream-routes.yaml
    max_size_bytes: 4096
    reload:
      enabled: true
      drain_timeout: 45s
      accepted_msg_id_ranges:
        - min: 2000
          max: 2999
        - min: 1001
          max: 1999
`)

	config, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if len(config.UpstreamRoutes) != 2 {
		t.Fatalf("UpstreamRoutes length = %d, want 2", len(config.UpstreamRoutes))
	}
	if config.UpstreamRoutes[1].HTTP == nil || config.UpstreamRoutes[1].HTTP.Token != "route-token" {
		t.Fatalf("HTTP route = %+v, want expanded token", config.UpstreamRoutes[1])
	}
	if config.UpstreamRoutesFile.Path != routesPath {
		t.Fatalf("UpstreamRoutesFile Path = %q, want %q", config.UpstreamRoutesFile.Path, routesPath)
	}
	if config.UpstreamRoutesFile.MaxSizeBytes != 4096 {
		t.Fatalf("UpstreamRoutesFile MaxSizeBytes = %d, want 4096", config.UpstreamRoutesFile.MaxSizeBytes)
	}
	reload := config.UpstreamRoutesFile.Reload
	if !reload.Enabled || reload.DrainTimeout != 45*time.Second {
		t.Fatalf("UpstreamRoutesFile Reload = %+v, want enabled with 45s drain", reload)
	}
	if len(reload.AcceptedMsgIDRanges) != 2 ||
		reload.AcceptedMsgIDRanges[0].Min != 1001 ||
		reload.AcceptedMsgIDRanges[0].Max != 1999 ||
		reload.AcceptedMsgIDRanges[1].Min != 2000 ||
		reload.AcceptedMsgIDRanges[1].Max != 2999 {
		t.Fatalf("AcceptedMsgIDRanges = %+v, want sorted ranges", reload.AcceptedMsgIDRanges)
	}
	if config.UpstreamRoutesFile.Loader == nil {
		t.Fatal("UpstreamRoutesFile Loader = nil, want reload loader")
	}

	writeTestFile(t, routesPath, `
version: 1
routes:
  - name: replacement-http
    msg_id_min: 1001
    msg_id_max: 1001
    target:
      type: http
      url: http://replacement.local/gateway/upstream
`)
	snapshot, err := config.UpstreamRoutesFile.Loader(context.Background())
	if err != nil {
		t.Fatalf("UpstreamRoutesFile Loader() error = %v", err)
	}
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Name != "replacement-http" {
		t.Fatalf("UpstreamRoutesFile Loader() routes = %+v", snapshot.Routes)
	}
}

func TestUpstreamRoutesFileUsesDefaultsWithoutReload(t *testing.T) {
	directory := t.TempDir()
	routesPath := filepath.Join(directory, "routes.yaml")
	writeTestFile(t, routesPath, "version: 1\nroutes: []\n")
	configPath := filepath.Join(directory, "z-courier.yaml")
	writeTestFile(t, configPath, `
upstream:
  routes_file:
    path: routes.yaml
`)

	config, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.UpstreamRoutesFile.MaxSizeBytes != defaultUpstreamRoutesFileMaxSize {
		t.Fatalf(
			"UpstreamRoutesFile MaxSizeBytes = %d, want %d",
			config.UpstreamRoutesFile.MaxSizeBytes,
			defaultUpstreamRoutesFileMaxSize,
		)
	}
	if config.UpstreamRoutesFile.Reload.Enabled {
		t.Fatal("UpstreamRoutesFile Reload Enabled = true, want false")
	}
}

func TestUpstreamRoutesFileRejectsInvalidSourceContract(t *testing.T) {
	tests := []struct {
		name       string
		mainConfig string
		routes     string
		want       string
	}{
		{
			name: "inline and file",
			mainConfig: `
upstream:
  routes:
    - name: inline
      msg_id_min: 1001
      target:
        type: http
        url: http://backend.local/upstream
  routes_file:
    path: routes.yaml
`,
			want: "upstream.routes and upstream.routes_file are mutually exclusive",
		},
		{
			name: "path required",
			mainConfig: `
upstream:
  routes_file:
    max_size_bytes: 1024
`,
			want: "upstream.routes_file.path is required",
		},
		{
			name: "empty file config",
			mainConfig: `
upstream:
  routes_file: {}
`,
			want: "upstream.routes_file.path is required",
		},
		{
			name: "reload settings disabled",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      drain_timeout: 10s
`,
			routes: "version: 1\nroutes: []\n",
			want:   "reload settings require enabled: true",
		},
		{
			name: "reload ranges required",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      enabled: true
`,
			routes: "version: 1\nroutes: []\n",
			want:   "accepted_msg_id_ranges is required",
		},
		{
			name: "drain timeout bounded",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      enabled: true
      drain_timeout: 20m
      accepted_msg_id_ranges:
        - min: 1001
          max: 1002
`,
			routes: "version: 1\nroutes: []\n",
			want:   "drain_timeout must be between",
		},
		{
			name: "reserved msg id",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      enabled: true
      accepted_msg_id_ranges:
        - min: 999
          max: 1001
`,
			routes: "version: 1\nroutes: []\n",
			want:   "uses reserved msg_id 1000",
		},
		{
			name: "overlapping ranges",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      enabled: true
      accepted_msg_id_ranges:
        - min: 1001
          max: 1100
        - min: 1100
          max: 1200
`,
			routes: "version: 1\nroutes: []\n",
			want:   "accepted range 1100-1200 overlaps 1001-1100",
		},
		{
			name: "route outside accepted ranges",
			mainConfig: `
upstream:
  routes_file:
    path: routes.yaml
    reload:
      enabled: true
      accepted_msg_id_ranges:
        - min: 1001
          max: 1999
`,
			routes: `
version: 1
routes:
  - name: outside
    msg_id_min: 2000
    msg_id_max: 2001
    target:
      type: http
      url: http://backend.local/upstream
`,
			want: "range 2000-2001 is outside reload accepted_msg_id_ranges",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if test.routes != "" {
				writeTestFile(t, filepath.Join(directory, "routes.yaml"), test.routes)
			}
			configPath := filepath.Join(directory, "z-courier.yaml")
			writeTestFile(t, configPath, test.mainConfig)

			_, err := LoadServerConfig(configPath)
			if err == nil {
				t.Fatal("LoadServerConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadServerConfig() error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestUpstreamRoutesFileRejectsInvalidDocument(t *testing.T) {
	tests := []struct {
		name   string
		routes string
		want   string
	}{
		{
			name:   "missing version",
			routes: "routes: []\n",
			want:   "version must be 1",
		},
		{
			name:   "unsupported version",
			routes: "version: 2\nroutes: []\n",
			want:   "version must be 1",
		},
		{
			name:   "unknown field",
			routes: "version: 1\nunknown: true\nroutes: []\n",
			want:   "field unknown not found",
		},
		{
			name:   "multiple documents",
			routes: "version: 1\nroutes: []\n---\nversion: 1\nroutes: []\n",
			want:   "multiple YAML documents are not allowed",
		},
		{
			name: "duplicate route name",
			routes: `
version: 1
routes:
  - name: duplicate
    enabled: false
  - name: duplicate
    enabled: false
`,
			want: `route "duplicate" duplicates route #1`,
		},
		{
			name: "surrounding route name whitespace",
			routes: `
version: 1
routes:
  - name: " spaced "
    enabled: false
`,
			want: "name must not have surrounding whitespace",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeTestFile(t, filepath.Join(directory, "routes.yaml"), test.routes)
			configPath := filepath.Join(directory, "z-courier.yaml")
			writeTestFile(t, configPath, "upstream:\n  routes_file:\n    path: routes.yaml\n")

			_, err := LoadServerConfig(configPath)
			if err == nil {
				t.Fatal("LoadServerConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadServerConfig() error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestUpstreamRoutesFileRejectsOversizedAndMissingEnvironment(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		directory := t.TempDir()
		writeTestFile(t, filepath.Join(directory, "routes.yaml"), "version: 1\nroutes: []\n")
		configPath := filepath.Join(directory, "z-courier.yaml")
		writeTestFile(t, configPath, `
upstream:
  routes_file:
    path: routes.yaml
    max_size_bytes: 8
`)

		_, err := LoadServerConfig(configPath)
		if err == nil || !strings.Contains(err.Error(), "exceeds max_size_bytes 8") {
			t.Fatalf("LoadServerConfig() error = %v, want oversized error", err)
		}
	})

	t.Run("missing environment", func(t *testing.T) {
		os.Unsetenv("ZCOURIER_TEST_MISSING_ROUTE_TOKEN")
		directory := t.TempDir()
		writeTestFile(t, filepath.Join(directory, "routes.yaml"), `
version: 1
routes:
  - name: backend
    msg_id_min: 1001
    target:
      type: http
      url: http://backend.local/upstream
      token: ${ZCOURIER_TEST_MISSING_ROUTE_TOKEN}
`)
		configPath := filepath.Join(directory, "z-courier.yaml")
		writeTestFile(t, configPath, "upstream:\n  routes_file:\n    path: routes.yaml\n")

		_, err := LoadServerConfig(configPath)
		if err == nil || !strings.Contains(err.Error(), "missing environment variables: ZCOURIER_TEST_MISSING_ROUTE_TOKEN") {
			t.Fatalf("LoadServerConfig() error = %v, want missing environment error", err)
		}
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
