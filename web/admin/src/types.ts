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

export type AdminHTTPRoute = {
  url: string;
  timeout?: string;
};

export type AdminNSQRoute = {
  addresses?: string[];
  topic: string;
  dial_timeout?: string;
  read_timeout?: string;
  write_timeout?: string;
  publish_mode?: string;
  retry_attempts?: number;
};

export type AdminRoute = {
  name: string;
  msg_id_min: number;
  msg_id_max?: number;
  target_type: string;
  max_in_flight?: number;
  http?: AdminHTTPRoute;
  nsq?: AdminNSQRoute;
};

export type AdminRoutes = {
  code: string;
  gateway_node: string;
  total: number;
  routes: AdminRoute[];
};

export type RuntimeDiagnostics = {
  started: boolean;
  started_at?: string;
  uptime?: string;
};

export type AuthDiagnostics = {
  provider: string;
  cache_wrapped?: boolean;
  verifier_loaded: boolean;
};

export type UpstreamRouteRuntime = {
  name: string;
  target_type: string;
  status: string;
  consecutive_failures?: number;
  last_reason?: string;
  last_failure_at?: string;
  last_success_at?: string;
  updated_at?: string;
};

export type UpstreamDiagnostics = {
  routes: number;
  http_routes: number;
  nsq_routes: number;
  routes_with_capacity_limit: number;
  http_route_states?: UpstreamRouteRuntime[];
};

export type CapacityDiagnostics = {
  internal_http_max_in_flight?: number;
  upstream_limited_routes?: number;
  rate_limit_enabled: boolean;
  rate_limit_max_requests?: number;
  rate_limit_window?: string;
};

export type DiagnosticWarning = {
  code: string;
  message: string;
};

export type AdminDiagnostics = {
  code: string;
  gateway_node: string;
  runtime: RuntimeDiagnostics;
  readiness: Readiness;
  sessions: SessionSummary;
  auth: AuthDiagnostics;
  internal_http: InternalHTTPSummary;
  cluster: ClusterSummary;
  downlink: DownlinkSummary;
  upstream: UpstreamDiagnostics;
  capacity: CapacityDiagnostics;
  dependencies: Dependency[];
  warnings?: DiagnosticWarning[];
};

export type MessageStatus = "pending" | "sent" | "delivered" | "failed" | "discarded";

export type MessageStatusResponse = {
  code: string;
  reason?: string;
  message_id?: string;
  client_id?: string;
  device_id?: string;
  msg_id?: number;
  trace_id?: string;
  session_id?: string;
  status?: MessageStatus;
  attempts?: number;
  last_error?: string;
  next_retry_at?: string | null;
  claim_owner?: string;
  claim_until?: string | null;
  created_at?: string | null;
  updated_at?: string | null;
  sent_at?: string | null;
  delivered_at?: string | null;
  ack_required?: boolean;
  body_size_bytes?: number;
};

export type AdminMessages = {
  code: string;
  reason?: string;
  status?: MessageStatus;
  limit?: number;
  total: number;
  messages?: MessageStatusResponse[];
};
