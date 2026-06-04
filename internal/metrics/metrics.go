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

	sessionsOnline = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "z_courier_sessions_online",
			Help: "Current number of online gateway sessions.",
		},
	)

	downlinkPush = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_downlink_push_total",
			Help: "Total number of downlink push requests handled by the gateway.",
		},
		[]string{"msg_id", "result"},
	)

	rateLimitRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "z_courier_rate_limit_rejected_total",
			Help: "Total number of ingress packets rejected by rate limiting.",
		},
		[]string{"msg_id"},
	)
)

func Handler() http.Handler {
	return promhttp.Handler()
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

func SetSessionsOnline(count int) {
	sessionsOnline.Set(float64(count))
}

func RecordDownlinkPush(msgID uint32, result string) {
	downlinkPush.WithLabelValues(formatMsgID(msgID), nonEmpty(result, "unknown")).Inc()
}

func RecordRateLimitRejected(msgID uint32) {
	rateLimitRejected.WithLabelValues(formatMsgID(msgID)).Inc()
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
