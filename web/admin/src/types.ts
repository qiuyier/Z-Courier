export type Readiness = {
  ready: boolean;
  status: string;
  draining_since?: string;
  drain_duration?: string;
};

export type SessionSummary = {
  online: number;
  unique_clients: number;
};

export type ClusterSummary = {
  enabled: boolean;
  internal_addr?: string;
  registry_type?: string;
  registry_ttl?: string;
  route_refresh_interval?: string;
  peer_auth_mode?: string;
};

export type InternalHTTPSummary = {
  enabled: boolean;
  addr?: string;
  auth_mode?: string;
  max_request_body_size?: number;
  max_in_flight?: number;
};

export type DownlinkSummary = {
  storage_type: string;
  store_configured: boolean;
  retry_interval?: string;
  retry_delay?: string;
  retry_jitter?: string;
  ack_timeout?: string;
  retry_lease?: string;
  max_attempts?: number;
  scan_limit?: number;
  bind_flush_limit?: number;
};

export type UpstreamSummary = {
  routes: number;
};

export type Dependency = {
  name: string;
  status: string;
  reason?: string;
};

export type AdminOverview = {
  code: string;
  gateway_node: string;
  readiness: Readiness;
  sessions: SessionSummary;
  cluster: ClusterSummary;
  internal_http: InternalHTTPSummary;
  downlink: DownlinkSummary;
  upstream: UpstreamSummary;
  dependencies: Dependency[];
};
