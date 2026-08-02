package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

var (
	authVerify = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_auth_verify_total",
			Help: "Total number of client token verification attempts.",
		},
		[]string{"provider", "result"},
	)

	authVerifyDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_auth_verify_duration_seconds",
			Help:    "Duration of client token verification attempts in seconds.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"provider", "result"},
	)

	authInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_auth_inflight",
			Help: "Current number of in-flight client token verification attempts.",
		},
		[]string{"provider"},
	)

	authCache = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_auth_cache_total",
			Help: "Total number of authentication cache lookups.",
		},
		[]string{"provider", "result"},
	)

	authJWKSRefresh = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_auth_jwks_refresh_total",
			Help: "Total number of JWT JWKS refresh attempts.",
		},
		[]string{"result"},
	)

	authJWKSRefreshDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_auth_jwks_refresh_duration_seconds",
			Help:    "Duration of JWT JWKS refresh attempts in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"result"},
	)

	adminPermissionRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_permission_rejected_total",
			Help: "Total number of admin session requests rejected by role permission checks.",
		},
		[]string{"role", "permission"},
	)

	adminCSRFRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_csrf_rejected_total",
			Help: "Total number of admin session mutation requests rejected by CSRF checks.",
		},
		[]string{"reason"},
	)

	adminAction = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_action_total",
			Help: "Total number of audited admin action attempts.",
		},
		[]string{"action", "result"},
	)

	adminSessionDisconnect = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_session_disconnect_total",
			Help: "Total number of admin local session disconnect attempts.",
		},
		[]string{"result"},
	)

	adminDownlinkTestPush = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_downlink_test_push_total",
			Help: "Total number of admin console downlink test push attempts.",
		},
		[]string{"result"},
	)

	adminRetryScan = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_retry_scan_total",
			Help: "Total number of admin-triggered downlink retry scan attempts.",
		},
		[]string{"result"},
	)

	adminAuditWrite = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_audit_write_total",
			Help: "Total number of admin audit store write attempts.",
		},
		[]string{"store", "result"},
	)

	adminAuditWriteDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_admin_audit_write_duration_seconds",
			Help:    "Duration of admin audit store write attempts in seconds.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"store", "result"},
	)

	adminSessionStoreOperation = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_admin_session_store_operation_total",
			Help: "Total number of admin session store operations.",
		},
		[]string{"store", "operation", "result"},
	)

	adminSessionStoreOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_admin_session_store_operation_duration_seconds",
			Help:    "Duration of admin session store operations in seconds.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"store", "operation", "result"},
	)

	gatewayReadiness = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_gateway_readiness",
			Help: "Current gateway readiness state as a one-hot gauge.",
		},
		[]string{"status"},
	)

	ingressPackets = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_ingress_packets_total",
			Help: "Total number of ingress packets handled by the gateway.",
		},
		[]string{"msg_id", "result"},
	)

	ingressRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_ingress_rejected_total",
			Help: "Total number of ingress packets rejected by the gateway.",
		},
		[]string{"msg_id", "ack_code"},
	)

	upstreamForward = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_forward_total",
			Help: "Total number of upstream forwarding attempts.",
		},
		[]string{"route", "target_type", "result"},
	)

	upstreamForwardDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_upstream_forward_duration_seconds",
			Help:    "Duration of upstream forwarding attempts in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"route", "target_type", "result"},
	)

	upstreamInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_upstream_inflight",
			Help: "Current number of in-flight upstream forwarding requests.",
		},
		[]string{"route", "target_type"},
	)

	upstreamOverloadRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_overload_rejected_total",
			Help: "Total number of upstream forwarding attempts rejected by in-flight capacity limits.",
		},
		[]string{"route", "target_type"},
	)

	upstreamRouteDegraded = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_upstream_route_degraded",
			Help: "Whether an upstream route is currently degraded or unavailable after consecutive failures.",
		},
		[]string{"route", "target_type"},
	)

	upstreamDiscoveryRefresh = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_discovery_refresh_total",
			Help: "Total number of upstream endpoint discovery refresh attempts.",
		},
		[]string{"route", "discovery_type", "result"},
	)

	upstreamDiscoveryRefreshDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_upstream_discovery_refresh_duration_seconds",
			Help:    "Duration of upstream endpoint discovery refresh attempts in seconds.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"route", "discovery_type", "result"},
	)

	upstreamDiscoveryResolvedEndpoints = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_upstream_discovery_resolved_endpoints",
			Help: "Current number of endpoints in the active upstream discovery snapshot.",
		},
		[]string{"route", "discovery_type"},
	)

	upstreamEndpointSelection = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_endpoint_selection_total",
			Help: "Total number of upstream endpoint selection attempts.",
		},
		[]string{"route", "discovery_type", "result"},
	)

	upstreamEndpointCooldownSkipped = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_endpoint_cooldown_skipped_total",
			Help: "Total number of upstream endpoints skipped while in failure cooldown.",
		},
		[]string{"route", "discovery_type"},
	)

	upstreamEndpointUnhealthy = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_upstream_endpoint_unhealthy",
			Help: "Current number of upstream endpoints marked unhealthy by the local selector.",
		},
		[]string{"route", "discovery_type"},
	)

	upstreamEndpointFailure = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_endpoint_failure_total",
			Help: "Total number of upstream endpoint attempts that failed.",
		},
		[]string{"route", "discovery_type", "failure_class"},
	)

	upstreamDiscoveryAttempts = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_upstream_discovery_attempts",
			Help:    "Number of endpoint attempts used by each discovery-backed upstream forward.",
			Buckets: []float64{0, 1, 2, 3, 4},
		},
		[]string{"route", "discovery_type", "result"},
	)

	upstreamFailover = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_upstream_failover_total",
			Help: "Total number of terminal upstream failover decisions.",
		},
		[]string{"route", "discovery_type", "decision"},
	)

	routeReload = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_route_reload_total",
			Help: "Total number of upstream route validation and reload operations.",
		},
		[]string{"trigger", "result"},
	)

	routeReloadDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_route_reload_duration_seconds",
			Help:    "Duration of upstream route validation and reload operations in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"result"},
	)

	routeGeneration = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_route_generation",
			Help: "Current active upstream route generation number.",
		},
	)

	routeRetiringGenerations = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_route_retiring_generations",
			Help: "Current number of upstream route generations waiting for in-flight requests to drain.",
		},
	)

	routeReloadLastSuccessTimestamp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_route_reload_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successfully activated upstream route generation.",
		},
	)

	routeRetirementDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "z_courier_route_retirement_duration_seconds",
			Help:    "Duration required for a retired upstream route generation to drain and close.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
		},
	)

	routeRetirementStartedTimestamp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_route_retirement_started_timestamp_seconds",
			Help: "Unix timestamp when the current retiring upstream route generation started draining.",
		},
	)

	routeRetirementTimeout = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_route_retirement_timeout_seconds",
			Help: "Configured drain timeout for upstream route generation retirement.",
		},
	)

	sessionsOnline = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_sessions_online",
			Help: "Current number of online gateway sessions.",
		},
	)

	clientsOnline = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_clients_online",
			Help: "Current number of unique online client IDs on this gateway.",
		},
	)

	downlinkPush = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_push_total",
			Help: "Total number of downlink push requests handled by the gateway.",
		},
		[]string{"msg_id", "result"},
	)

	downlinkQueueCapacityRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_queue_capacity_rejected_total",
			Help: "Total number of reliable downlink admissions rejected by queue capacity limits.",
		},
		[]string{"scope"},
	)

	internalHTTPInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_internal_http_inflight",
			Help: "Current number of in-flight protected internal HTTP requests.",
		},
		[]string{"path"},
	)

	internalHTTPOverloadRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_internal_http_overload_rejected_total",
			Help: "Total number of internal HTTP requests rejected by in-flight capacity limits.",
		},
		[]string{"path"},
	)

	internalHTTPSignature = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_internal_http_signature_total",
			Help: "Total number of internal HTTP HMAC verification attempts.",
		},
		[]string{"result"},
	)

	downlinkAck = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_ack_total",
			Help: "Total number of downlink ACK packets handled by the gateway.",
		},
		[]string{"msg_id", "result"},
	)

	downlinkAckLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_ack_latency_seconds",
			Help:    "Latency from downlink send time to client ACK time in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"msg_id"},
	)

	downlinkRequeue = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_requeue_total",
			Help: "Total number of manual downlink message requeue attempts.",
		},
		[]string{"result"},
	)

	downlinkBulkRequeue = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_bulk_requeue_total",
			Help: "Total number of guarded bulk downlink requeue requests.",
		},
		[]string{"result"},
	)

	downlinkDiscard = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_discard_total",
			Help: "Total number of manual downlink message discard attempts.",
		},
		[]string{"result"},
	)

	downlinkCleanup = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_cleanup_total",
			Help: "Total number of downlink retention cleanup attempts.",
		},
		[]string{"status", "result"},
	)

	downlinkCleanupDeleted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_cleanup_deleted_total",
			Help: "Total number of expired downlink messages deleted by retention cleanup.",
		},
		[]string{"status"},
	)

	downlinkCleanupDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_cleanup_duration_seconds",
			Help:    "Duration of downlink retention cleanup runs in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"result"},
	)

	rateLimitRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_rate_limit_rejected_total",
			Help: "Total number of ingress packets rejected by rate limiting.",
		},
		[]string{"msg_id"},
	)

	trafficPolicySelection = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_traffic_policy_selection_total",
			Help: "Total number of named traffic policy selection outcomes.",
		},
		[]string{"mode", "policy", "result"},
	)

	trafficPolicyQuotaStore = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_traffic_policy_quota_store_total",
			Help: "Total number of traffic policy quota store admission decisions.",
		},
		[]string{"mode", "policy", "key_scope", "result"},
	)

	trafficPolicyQuotaStoreDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_traffic_policy_quota_store_duration_seconds",
			Help:    "Duration of traffic policy quota store admission decisions in seconds.",
			Buckets: []float64{0.00001, 0.00005, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"mode", "policy", "key_scope", "result"},
	)

	trafficPolicyLocalKeys = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_traffic_policy_local_keys",
			Help: "Current number of live keys in the local traffic policy quota store.",
		},
		[]string{"mode"},
	)

	trafficPolicyLocalKeyLimit = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "z_courier_traffic_policy_local_key_limit",
			Help: "Configured maximum number of live keys in the local traffic policy quota store.",
		},
		[]string{"mode"},
	)

	clusterRegistryBind = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_registry_bind_total",
			Help: "Total number of cluster registry bind attempts.",
		},
		[]string{"result"},
	)

	clusterRegistryUnbind = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_registry_unbind_total",
			Help: "Total number of cluster registry unbind attempts.",
		},
		[]string{"result"},
	)

	clusterRegistryLookup = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_registry_lookup_total",
			Help: "Total number of cluster registry lookup attempts.",
		},
		[]string{"result"},
	)

	clusterRegistryTouch = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_registry_touch_total",
			Help: "Total number of cluster registry touch attempts.",
		},
		[]string{"result"},
	)

	clusterPeerPush = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_peer_push_total",
			Help: "Total number of cluster peer push attempts.",
		},
		[]string{"target_node", "result"},
	)

	clusterPeerPushDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_cluster_peer_push_duration_seconds",
			Help:    "Duration of cluster peer push attempts in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"target_node", "result"},
	)

	clusterPeerSignature = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_peer_signature_total",
			Help: "Total number of cluster peer HMAC verification attempts.",
		},
		[]string{"result"},
	)

	clusterStaleRoutes = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_cluster_stale_routes_total",
			Help: "Total number of stale cluster routes detected by the gateway.",
		},
		[]string{"reason"},
	)

	downlinkRetryScan = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_retry_scan_total",
			Help: "Total number of downlink retry scans.",
		},
		[]string{"result"},
	)

	downlinkRetryScanDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_retry_scan_duration_seconds",
			Help:    "Duration of downlink retry scans in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"result"},
	)

	downlinkRetryMessages = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_retry_messages_total",
			Help: "Total number of messages processed by the downlink retry worker.",
		},
		[]string{"result"},
	)

	downlinkRetryClaimMessages = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_retry_claim_messages_total",
			Help: "Total number of messages claimed by the downlink retry worker.",
		},
		[]string{"owner", "result"},
	)

	downlinkRetryClaimDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_retry_claim_duration_seconds",
			Help:    "Duration of downlink retry claim attempts in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"owner", "result"},
	)

	downlinkRetrySelectedDevices = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_retry_selected_devices",
			Help:    "Number of unique client and device pairs selected by one downlink retry scan.",
			Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{"mode"},
	)

	downlinkRetryMaxPerDevice = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "z_courier_downlink_retry_max_per_device",
			Help:    "Maximum number of messages selected for one client and device pair in a retry scan.",
			Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
		[]string{"mode"},
	)
)

func Handler() http.Handler {
	return promhttp.Handler()
}

func RecordAuthVerify(provider, result string, duration time.Duration) {
	labels := []string{nonEmpty(provider, "custom"), nonEmpty(result, "error")}
	authVerify.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		authVerifyDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func AddAuthInFlight(provider string, delta float64) {
	authInFlight.WithLabelValues(nonEmpty(provider, "custom")).Add(delta)
}

func RecordAuthCache(provider, result string) {
	authCache.WithLabelValues(nonEmpty(provider, "custom"), nonEmpty(result, "miss")).Inc()
}

func RecordAuthJWKSRefresh(result string, duration time.Duration) {
	result = nonEmpty(result, "error")
	authJWKSRefresh.WithLabelValues(result).Inc()
	if duration >= 0 {
		authJWKSRefreshDuration.WithLabelValues(result).Observe(duration.Seconds())
	}
}

func RecordAdminPermissionRejected(role string, permission string) {
	adminPermissionRejected.WithLabelValues(nonEmpty(role, "unknown"), nonEmpty(permission, "unknown")).Inc()
}

func RecordAdminCSRFRejected(reason string) {
	adminCSRFRejected.WithLabelValues(nonEmpty(reason, "unknown")).Inc()
}

func RecordAdminAction(action string, result string) {
	adminAction.WithLabelValues(nonEmpty(action, "unknown"), nonEmpty(result, "unknown")).Inc()
}

func RecordAdminSessionDisconnect(result string) {
	adminSessionDisconnect.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordAdminDownlinkTestPush(result string) {
	adminDownlinkTestPush.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func SetGatewayReadiness(status string) {
	if status != "draining" {
		status = "ready"
	}
	for _, candidate := range []string{"ready", "draining"} {
		value := 0.0
		if candidate == status {
			value = 1
		}
		gatewayReadiness.WithLabelValues(candidate).Set(value)
	}
}

func RecordIngressPacket(msgID uint32, result string) {
	ingressPackets.WithLabelValues(formatMsgID(msgID), nonEmpty(result, "unknown")).Inc()
}

func RecordIngressRejected(msgID uint32, code protocol.AckCode) {
	ingressRejected.WithLabelValues(formatMsgID(msgID), string(code)).Inc()
}

func RecordUpstreamForward(route, targetType, result string, duration time.Duration) {
	labels := []string{nonEmpty(route, "unknown"), nonEmpty(targetType, "unknown"), nonEmpty(result, "unknown")}
	upstreamForward.WithLabelValues(labels...).Inc()
	upstreamForwardDuration.WithLabelValues(labels...).Observe(duration.Seconds())
}

func AddUpstreamInFlight(route, targetType string, delta float64) {
	upstreamInFlight.WithLabelValues(nonEmpty(route, "unknown"), nonEmpty(targetType, "unknown")).Add(delta)
}

func RecordUpstreamOverloadRejected(route, targetType string) {
	upstreamOverloadRejected.WithLabelValues(nonEmpty(route, "unknown"), nonEmpty(targetType, "unknown")).Inc()
}

func SetUpstreamRouteDegraded(route, targetType string, degraded bool) {
	value := 0.0
	if degraded {
		value = 1
	}
	upstreamRouteDegraded.WithLabelValues(nonEmpty(route, "unknown"), nonEmpty(targetType, "unknown")).Set(value)
}

func DeleteUpstreamRouteMutableMetrics(route, targetType string) {
	labels := []string{nonEmpty(route, "unknown"), nonEmpty(targetType, "unknown")}
	upstreamInFlight.DeleteLabelValues(labels...)
	upstreamRouteDegraded.DeleteLabelValues(labels...)
}

func RecordUpstreamDiscoveryRefresh(route, discoveryType, result string, duration time.Duration) {
	labels := []string{
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
		nonEmpty(result, "unknown"),
	}
	upstreamDiscoveryRefresh.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		upstreamDiscoveryRefreshDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func SetUpstreamDiscoveryResolvedEndpoints(route, discoveryType string, count int) {
	if count < 0 {
		count = 0
	}
	upstreamDiscoveryResolvedEndpoints.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
	).Set(float64(count))
}

func RecordUpstreamEndpointSelection(route, discoveryType, result string) {
	upstreamEndpointSelection.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
		nonEmpty(result, "unknown"),
	).Inc()
}

func RecordUpstreamEndpointCooldownSkipped(route, discoveryType string, count int) {
	addCounter(upstreamEndpointCooldownSkipped.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
	), count)
}

func SetUpstreamEndpointUnhealthy(route, discoveryType string, count int) {
	if count < 0 {
		count = 0
	}
	upstreamEndpointUnhealthy.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
	).Set(float64(count))
}

func DeleteUpstreamDiscoveryMutableMetrics(route, discoveryType string) {
	labels := []string{nonEmpty(route, "unknown"), nonEmpty(discoveryType, "unknown")}
	upstreamDiscoveryResolvedEndpoints.DeleteLabelValues(labels...)
	upstreamEndpointUnhealthy.DeleteLabelValues(labels...)
}

func RecordUpstreamEndpointFailure(route, discoveryType, failureClass string) {
	upstreamEndpointFailure.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
		nonEmpty(failureClass, "unknown"),
	).Inc()
}

func ObserveUpstreamDiscoveryAttempts(route, discoveryType, result string, attempts int) {
	if attempts < 0 {
		attempts = 0
	}
	upstreamDiscoveryAttempts.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
		nonEmpty(result, "unknown"),
	).Observe(float64(attempts))
}

func RecordUpstreamFailoverDecision(route, discoveryType, decision string) {
	upstreamFailover.WithLabelValues(
		nonEmpty(route, "unknown"),
		nonEmpty(discoveryType, "unknown"),
		nonEmpty(decision, "unknown"),
	).Inc()
}

func RecordRouteReload(trigger, result string, duration time.Duration, completedAt time.Time) {
	trigger = routeReloadTriggerLabel(trigger)
	result = routeReloadResultLabel(result)
	routeReload.WithLabelValues(trigger, result).Inc()
	if duration >= 0 {
		routeReloadDuration.WithLabelValues(result).Observe(duration.Seconds())
	}
	if result == "reloaded" && !completedAt.IsZero() {
		routeReloadLastSuccessTimestamp.Set(float64(completedAt.Unix()))
	}
}

func SetRouteGeneration(generation uint64) {
	routeGeneration.Set(float64(generation))
}

func SetRouteRetiringGenerations(count int) {
	if count < 0 {
		count = 0
	}
	routeRetiringGenerations.Set(float64(count))
}

func ObserveRouteRetirementDuration(duration time.Duration) {
	if duration < 0 {
		return
	}
	routeRetirementDuration.Observe(duration.Seconds())
}

func SetRouteRetirement(startedAt time.Time, timeout time.Duration) {
	if startedAt.IsZero() {
		routeRetirementStartedTimestamp.Set(0)
	} else {
		routeRetirementStartedTimestamp.Set(float64(startedAt.Unix()))
	}
	if timeout < 0 {
		timeout = 0
	}
	routeRetirementTimeout.Set(timeout.Seconds())
}

func ClearRouteRetirement() {
	routeRetirementStartedTimestamp.Set(0)
}

func SetSessionsOnline(count int) {
	sessionsOnline.Set(float64(count))
}

func SetClientsOnline(count int) {
	clientsOnline.Set(float64(count))
}

func RecordDownlinkPush(msgID uint32, result string) {
	downlinkPush.WithLabelValues(formatMsgID(msgID), nonEmpty(result, "unknown")).Inc()
}

func RecordDownlinkQueueCapacityRejected(scope string) {
	downlinkQueueCapacityRejected.WithLabelValues(nonEmpty(scope, "unknown")).Inc()
}

func AddInternalHTTPInFlight(path string, delta float64) {
	internalHTTPInFlight.WithLabelValues(nonEmpty(path, "unknown")).Add(delta)
}

func RecordInternalHTTPOverloadRejected(path string) {
	internalHTTPOverloadRejected.WithLabelValues(nonEmpty(path, "unknown")).Inc()
}

func RecordInternalHTTPSignature(result string) {
	internalHTTPSignature.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordDownlinkAck(msgID uint32, result string) {
	downlinkAck.WithLabelValues(formatMsgID(msgID), nonEmpty(result, "unknown")).Inc()
}

func ObserveDownlinkAckLatency(msgID uint32, duration time.Duration) {
	if duration < 0 {
		return
	}

	downlinkAckLatency.WithLabelValues(formatMsgID(msgID)).Observe(duration.Seconds())
}

func RecordDownlinkRequeue(result string) {
	downlinkRequeue.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordDownlinkBulkRequeue(result string) {
	downlinkBulkRequeue.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordDownlinkDiscard(result string) {
	downlinkDiscard.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordAdminRetryScan(result string) {
	adminRetryScan.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordAdminAuditWrite(store, result string, duration time.Duration) {
	labels := []string{nonEmpty(store, "unknown"), nonEmpty(result, "unknown")}
	adminAuditWrite.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		adminAuditWriteDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func RecordAdminSessionStoreOperation(store, operation, result string, duration time.Duration) {
	labels := []string{nonEmpty(store, "unknown"), nonEmpty(operation, "unknown"), nonEmpty(result, "unknown")}
	adminSessionStoreOperation.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		adminSessionStoreOperationDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func RecordDownlinkCleanupStatus(status, result string, deleted int) {
	status = nonEmpty(status, "unknown")
	downlinkCleanup.WithLabelValues(status, nonEmpty(result, "unknown")).Inc()
	addCounter(downlinkCleanupDeleted.WithLabelValues(status), deleted)
}

func RecordDownlinkCleanupDuration(result string, duration time.Duration) {
	label := nonEmpty(result, "unknown")
	if duration >= 0 {
		downlinkCleanupDuration.WithLabelValues(label).Observe(duration.Seconds())
	}
}

func RecordRateLimitRejected(msgID uint32) {
	rateLimitRejected.WithLabelValues(formatMsgID(msgID)).Inc()
}

func RecordTrafficPolicySelection(mode, policy, result string) {
	trafficPolicySelection.WithLabelValues(
		trafficPolicyModeLabel(mode),
		nonEmpty(policy, "none"),
		trafficPolicySelectionResultLabel(result),
	).Inc()
}

func RecordTrafficPolicyQuotaStore(
	mode string,
	policy string,
	keyScope string,
	result string,
	duration time.Duration,
) {
	labels := []string{
		trafficPolicyModeLabel(mode),
		nonEmpty(policy, "unknown"),
		trafficPolicyKeyScopeLabel(keyScope),
		trafficPolicyQuotaResultLabel(result),
	}
	trafficPolicyQuotaStore.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		trafficPolicyQuotaStoreDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func SetTrafficPolicyLocalKeys(count int) {
	if count < 0 {
		count = 0
	}
	trafficPolicyLocalKeys.WithLabelValues("local").Set(float64(count))
}

func SetTrafficPolicyLocalKeyLimit(limit int) {
	if limit < 0 {
		limit = 0
	}
	trafficPolicyLocalKeyLimit.WithLabelValues("local").Set(float64(limit))
}

func RecordClusterRegistryBind(result string) {
	clusterRegistryBind.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordClusterRegistryUnbind(result string) {
	clusterRegistryUnbind.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordClusterRegistryLookup(result string) {
	clusterRegistryLookup.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordClusterRegistryTouch(result string) {
	clusterRegistryTouch.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordClusterPeerPush(targetNode, result string, duration time.Duration) {
	labels := []string{nonEmpty(targetNode, "unknown"), nonEmpty(result, "unknown")}
	clusterPeerPush.WithLabelValues(labels...).Inc()
	if duration >= 0 {
		clusterPeerPushDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func RecordClusterPeerSignature(result string) {
	clusterPeerSignature.WithLabelValues(nonEmpty(result, "unknown")).Inc()
}

func RecordClusterStaleRoute(reason string) {
	clusterStaleRoutes.WithLabelValues(nonEmpty(reason, "unknown")).Inc()
}

func RecordDownlinkRetryScan(result string, duration time.Duration) {
	label := nonEmpty(result, "unknown")
	downlinkRetryScan.WithLabelValues(label).Inc()
	if duration >= 0 {
		downlinkRetryScanDuration.WithLabelValues(label).Observe(duration.Seconds())
	}
}

func RecordDownlinkRetryMessages(scanned, sent, queued, failed int) {
	addCounter(downlinkRetryMessages.WithLabelValues("scanned"), scanned)
	addCounter(downlinkRetryMessages.WithLabelValues("sent"), sent)
	addCounter(downlinkRetryMessages.WithLabelValues("queued"), queued)
	addCounter(downlinkRetryMessages.WithLabelValues("failed"), failed)
}

func RecordDownlinkRetryClaim(owner, result string, claimed int, duration time.Duration) {
	labels := []string{nonEmpty(owner, "unknown"), nonEmpty(result, "unknown")}
	addCounter(downlinkRetryClaimMessages.WithLabelValues(labels...), claimed)
	if duration >= 0 {
		downlinkRetryClaimDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	}
}

func RecordDownlinkRetrySelection(mode string, devices, maxPerDevice int) {
	label := nonEmpty(mode, "unknown")
	downlinkRetrySelectedDevices.WithLabelValues(label).Observe(float64(devices))
	downlinkRetryMaxPerDevice.WithLabelValues(label).Observe(float64(maxPerDevice))
}

func formatMsgID(msgID uint32) string {
	return strconv.FormatUint(uint64(msgID), 10)
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func trafficPolicyModeLabel(mode string) string {
	switch mode {
	case "local", "redis":
		return mode
	default:
		return "unknown"
	}
}

func trafficPolicySelectionResultLabel(result string) string {
	switch result {
	case "selected", "no_match":
		return result
	default:
		return "unknown"
	}
}

func trafficPolicyKeyScopeLabel(keyScope string) string {
	if keyScope == "client_id" {
		return keyScope
	}
	return "unknown"
}

func trafficPolicyQuotaResultLabel(result string) string {
	switch result {
	case "allowed", "rate_limited", "overloaded", "admission_unavailable":
		return result
	default:
		return "unknown"
	}
}

func routeReloadTriggerLabel(trigger string) string {
	switch trigger {
	case "admin_api", "sighup":
		return trigger
	default:
		return "unknown"
	}
}

func routeReloadResultLabel(result string) string {
	switch result {
	case "validated",
		"reloaded",
		"reload_disabled",
		"reload_busy",
		"generation_conflict",
		"source_read_failed",
		"parse_failed",
		"validation_failed",
		"candidate_load_failed",
		"candidate_build_failed",
		"reload_failed":
		return result
	default:
		return "unknown"
	}
}

func addCounter(counter prometheus.Counter, value int) {
	if value <= 0 {
		return
	}

	counter.Add(float64(value))
}
