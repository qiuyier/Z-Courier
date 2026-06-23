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

func TestLoadProductionClusterReferenceConfigs(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		node         string
		internalAddr string
	}{
		{
			name:         "gateway-a",
			path:         "../../deploy/production-cluster/config/gateway-a.yaml",
			node:         "gateway-prod-a",
			internalAddr: "http://gateway-a:18080",
		},
		{
			name:         "gateway-b",
			path:         "../../deploy/production-cluster/config/gateway-b.yaml",
			node:         "gateway-prod-b",
			internalAddr: "http://gateway-b:18080",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := LoadServerConfig(test.path)
			if err != nil {
				t.Fatalf("LoadServerConfig(%s) error = %v", test.path, err)
			}
			if config.GatewayNode != test.node {
				t.Fatalf("GatewayNode = %q, want %q", config.GatewayNode, test.node)
			}
			if !config.Cluster.Enabled {
				t.Fatal("Cluster.Enabled = false, want true")
			}
			if config.Cluster.InternalAddr != test.internalAddr {
				t.Fatalf("Cluster.InternalAddr = %q, want %q", config.Cluster.InternalAddr, test.internalAddr)
			}
			if config.Cluster.Registry.Type != "redis" {
				t.Fatalf("Cluster.Registry.Type = %q, want redis", config.Cluster.Registry.Type)
			}
			if config.Cluster.Peer.Auth.Mode != "hmac" {
				t.Fatalf("Cluster.Peer.Auth.Mode = %q, want hmac", config.Cluster.Peer.Auth.Mode)
			}
			if len(config.UpstreamRoutes) != 2 {
				t.Fatalf("len(UpstreamRoutes) = %d, want 2", len(config.UpstreamRoutes))
			}
		})
	}
}
