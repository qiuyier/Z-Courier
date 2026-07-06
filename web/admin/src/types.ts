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

export type AdminMonitoringLinks = {
  prometheus_url?: string;
  grafana_url?: string;
  dashboard_url?: string;
};

export type AdminConsoleSummary = {
  enabled: boolean;
  path?: string;
  assets_dir?: string;
  monitoring?: AdminMonitoringLinks;
  session?: {
    enabled: boolean;
    ttl?: string;
    cookie_name?: string;
    cookie_secure: boolean;
    cookie_same_site?: string;
    role?: string;
    permissions?: string[];
    storage_type?: string;
    redis_configured: boolean;
  };
  audit?: {
    storage_type: string;
    capacity?: number;
    store_configured: boolean;
    postgres_configured: boolean;
  };
};

export type AdminConsoleSession = {
  session_id: string;
  principal: string;
  role: string;
  permissions?: string[];
  created_at: string;
  expires_at: string;
  last_seen_at: string;
  expires_in_ms: number;
};

export type AdminSessionResponse = {
  code: string;
  reason?: string;
  gateway_node: string;
  session?: AdminConsoleSession;
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
  admin_console: AdminConsoleSummary;
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

export type AdminAuditEvent = {
  id: number;
  recorded_at: string;
  action: string;
  result: string;
  http_status?: number;
  gateway_node?: string;
  auth_mode?: string;
  principal?: string;
  role?: string;
  admin_session_id?: string;
  auth_key_id?: string;
  method?: string;
  path?: string;
  remote_addr?: string;
  permission?: string;
  target_client_id?: string;
  target_device_id?: string;
  target_session_id?: string;
  target_conn_id?: number;
  message_id?: string;
  trace_id?: string;
  reason?: string;
  details?: Record<string, string>;
};

export type AdminAudit = {
  code: string;
  reason?: string;
  gateway_node: string;
  limit: number;
  total: number;
  events: AdminAuditEvent[];
};

export type AdminSession = {
  session_id: string;
  conn_id: number;
  client_id: string;
  device_id: string;
  token_id?: string;
  gateway_node?: string;
  connected_at?: string;
  last_seen_at?: string;
};

export type AdminSessions = {
  code: string;
  reason?: string;
  gateway_node: string;
  session_id?: string;
  client_id?: string;
  device_id?: string;
  limit: number;
  total: number;
  unique_clients: number;
  sessions: AdminSession[];
};

export type AdminClusterRoute = {
  client_id: string;
  device_id: string;
  session_id: string;
  gateway_node: string;
  internal_addr?: string;
  token_id?: string;
  updated_at?: string;
  expires_at?: string;
  expires_in_ms?: number;
  local_route: boolean;
  local_session_found: boolean;
};

export type AdminClusterRoutes = {
  code: string;
  reason?: string;
  gateway_node: string;
  session_id?: string;
  client_id?: string;
  device_id?: string;
  limit: number;
  total: number;
  unique_clients: number;
  cluster_enabled: boolean;
  routes: AdminClusterRoute[];
};

export type AdminClientRouteLookup = {
  code: string;
  reason?: string;
  gateway_node: string;
  client_id?: string;
  device_id?: string;
  local_session_found: boolean;
  local_session?: AdminSession;
  cluster_enabled: boolean;
  cluster_route_found: boolean;
  cluster_route?: AdminClusterRoute;
};

export type AdminSessionDisconnectRequest = {
  session_id: string;
  client_id?: string;
  device_id?: string;
};

export type AdminSessionDisconnectResponse = {
  code: string;
  reason?: string;
  gateway_node: string;
  session_id?: string;
  conn_id?: number;
  client_id?: string;
  device_id?: string;
  local_session_found: boolean;
  disconnected: boolean;
  local_session?: AdminSession;
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
  admin_console: AdminConsoleSummary;
  cluster: ClusterSummary;
  downlink: DownlinkSummary;
  upstream: UpstreamDiagnostics;
  capacity: CapacityDiagnostics;
  dependencies: Dependency[];
  warnings?: DiagnosticWarning[];
};

export type AdminDiagnosisSection = {
  endpoint: string;
  http_status?: number;
  error?: string;
  body?: unknown;
};

export type AdminDiagnosisBundle = {
  code: string;
  reason?: string;
  generated_at?: string;
  target_url?: string;
  collection_status?: string;
  sections?: Record<string, AdminDiagnosisSection>;
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

export type DownlinkTestPushResponse = {
  code: string;
  reason?: string;
  delivery_state?: string;
  client_id?: string;
  device_id?: string;
  session_id?: string;
  conn_id?: number;
  message_id?: string;
  trace_id?: string;
};

export type AdminMessages = {
  code: string;
  reason?: string;
  status?: MessageStatus;
  limit?: number;
  total: number;
  messages?: MessageStatusResponse[];
};

export type RetryScanResponse = {
  code: string;
  reason?: string;
  limit?: number;
  scanned: number;
  sent: number;
  queued: number;
  failed: number;
};

export type AdminCheckStatus = "ok" | "degraded" | "failed" | "skipped";

export type AdminCheckResult = {
  name: string;
  status: AdminCheckStatus;
  target?: string;
  latency?: string;
  error?: string;
};

export type AdminCheck = {
  code: string;
  gateway_node: string;
  status: AdminCheckStatus;
  timeout?: string;
  checks: AdminCheckResult[];
};
