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

func SetSessionsOnline(count int) {
	sessionsOnline.Set(float64(count))
}

func SetClientsOnline(count int) {
	clientsOnline.Set(float64(count))
}

func RecordDownlinkPush(msgID uint32, result string) {
	downlinkPush.WithLabelValues(formatMsgID(msgID), nonEmpty(result, "unknown")).Inc()
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

func RecordDownlinkDiscard(result string) {
	downlinkDiscard.WithLabelValues(nonEmpty(result, "unknown")).Inc()
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

func formatMsgID(msgID uint32) string {
	return strconv.FormatUint(uint64(msgID), 10)
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func addCounter(counter prometheus.Counter, value int) {
	if value <= 0 {
		return
	}

	counter.Add(float64(value))
}
