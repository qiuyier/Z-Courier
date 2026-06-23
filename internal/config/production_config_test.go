package config

import "testing"

func TestLoadProductionReferenceConfig(t *testing.T) {
	config, err := LoadServerConfig("../../deploy/production/config/z-courier.yaml")
	if err != nil {
		t.Fatalf("LoadServerConfig(production reference) error = %v", err)
	}

	if config.InternalHTTPAddr != "0.0.0.0:18080" {
		t.Fatalf("InternalHTTPAddr = %q, want 0.0.0.0:18080", config.InternalHTTPAddr)
	}
	if config.DownlinkStorage.Type != "postgres" {
		t.Fatalf("DownlinkStorage.Type = %q, want postgres", config.DownlinkStorage.Type)
	}
	if len(config.UpstreamRoutes) != 2 {
		t.Fatalf("len(UpstreamRoutes) = %d, want 2", len(config.UpstreamRoutes))
	}
}
