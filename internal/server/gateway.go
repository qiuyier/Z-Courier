package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type Gateway struct {
	server   ziface.IServer
	sessions *session.Manager
	logger   *zap.Logger
	health   *gatewayHealth
	runtime  *gatewayRuntime
	started  atomic.Bool

	clusterRegistry           cluster.OnlineRegistry
	clusterRegistryCloser     io.Closer
	adminSessionCloser        io.Closer
	adminAuditCloser          io.Closer
	authVerifierCloser        io.Closer
	trafficPolicyCloser       io.Closer
	internalHTTP              *http.Server
	upstream                  *router.Engine
	downlink                  *downlink.Service
	downlinkCloser            io.Closer
	downlinkRetryInterval     time.Duration
	downlinkRetryScanLimit    int
	downlinkRetryFairness     downlink.RetryFairness
	downlinkWorkerCancel      context.CancelFunc
	downlinkWorkerCompleted   chan struct{}
	downlinkTerminalInterval  time.Duration
	downlinkTerminalScanLimit int
	downlinkTerminalCancel    context.CancelFunc
	downlinkTerminalCompleted chan struct{}
	downlinkRetention         downlink.RetentionPolicy
	downlinkCleanupInterval   time.Duration
	downlinkCleanupCancel     context.CancelFunc
	downlinkCleanupCompleted  chan struct{}
	clusterRouteRefresher     *clusterRouteRefresher
	clusterRefreshCancel      context.CancelFunc
	clusterRefreshCompleted   chan struct{}
	shutdownOnce              sync.Once
	shutdownErr               error
}

func New(config Config, logger *zap.Logger) (*Gateway, error) {
	if logger == nil {
		logger = defaultLogger()
	}

	config = normalizeConfig(config)
	authVerifierCloser, _ := config.Verifier.(io.Closer)
	deliveryPolicies, err := newDownlinkPolicySet(config)
	if err != nil {
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	msgIDs, err := registeredMsgIDs(config)
	if err != nil {
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	trafficPolicyHandler, trafficPolicyCloser, err := newTrafficPolicyHandler(config.Pipeline.TrafficPolicies)
	if err != nil {
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	if trafficPolicyHandler != nil {
		config.TrafficPolicyRuntime = trafficPolicyHandler.Runtime()
	}
	config.UpstreamRuntime = newUpstreamRuntime(config.UpstreamRoutes)
	upstream, err := newUpstreamEngine(config)
	if err != nil {
		closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	clusterRegistry, clusterRegistryCloser, err := newClusterRegistry(config)
	if err != nil {
		if upstream != nil {
			_ = upstream.Close()
		}
		closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	zServer := znet.NewServer()
	downlinkService, downlinkCloser, err := newDownlinkService(config, zServer.GetConnMgr(), clusterRegistry, deliveryPolicies)
	if err != nil {
		if upstream != nil {
			_ = upstream.Close()
		}
		if clusterRegistryCloser != nil {
			_ = clusterRegistryCloser.Close()
		}
		closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	adminAudit, adminAuditCloser, err := newAdminAuditStore(config)
	if err != nil {
		closeWithLog(downlinkCloser, logger, "downlink store")
		if upstream != nil {
			_ = upstream.Close()
		}
		if clusterRegistryCloser != nil {
			_ = clusterRegistryCloser.Close()
		}
		closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}
	config.AdminAudit = adminAudit
	var adminSessions *adminSessionManager
	var adminSessionCloser io.Closer
	if !config.DisableInternalHTTP && config.InternalHTTPAddr != "" {
		adminSessions, adminSessionCloser, err = newConfiguredAdminSessionManager(config.AdminConsole.Session)
		if err != nil {
			closeWithLog(adminAuditCloser, logger, "admin audit store")
			closeWithLog(downlinkCloser, logger, "downlink store")
			if upstream != nil {
				_ = upstream.Close()
			}
			if clusterRegistryCloser != nil {
				_ = clusterRegistryCloser.Close()
			}
			closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
			closeWithLog(authVerifierCloser, logger, "authentication verifier")
			return nil, err
		}
	}
	config.AdminSessions = adminSessions
	health := &gatewayHealth{}
	runtime := newGatewayRuntime()
	internalHTTP, err := newInternalHTTPServer(config, logger, downlinkService, health, clusterRegistry, runtime)
	if err != nil {
		closeWithLog(adminSessionCloser, logger, "admin session store")
		closeWithLog(adminAuditCloser, logger, "admin audit store")
		closeWithLog(downlinkCloser, logger, "downlink store")
		if upstream != nil {
			_ = upstream.Close()
		}
		if clusterRegistryCloser != nil {
			_ = clusterRegistryCloser.Close()
		}
		closeWithLog(trafficPolicyCloser, logger, "traffic policy quota store")
		closeWithLog(authVerifierCloser, logger, "authentication verifier")
		return nil, err
	}

	gateway := &Gateway{
		server:                    zServer,
		sessions:                  config.Sessions,
		logger:                    logger,
		health:                    health,
		runtime:                   runtime,
		clusterRegistry:           clusterRegistry,
		clusterRegistryCloser:     clusterRegistryCloser,
		adminSessionCloser:        adminSessionCloser,
		adminAuditCloser:          adminAuditCloser,
		authVerifierCloser:        authVerifierCloser,
		trafficPolicyCloser:       trafficPolicyCloser,
		internalHTTP:              internalHTTP,
		upstream:                  upstream,
		downlink:                  downlinkService,
		downlinkCloser:            downlinkCloser,
		downlinkRetryInterval:     config.DownlinkDelivery.RetryInterval,
		downlinkRetryScanLimit:    config.DownlinkDelivery.ScanLimit,
		downlinkRetryFairness:     config.DownlinkDelivery.RetryFairness,
		downlinkTerminalInterval:  config.DownlinkTerminal.RetryInterval,
		downlinkTerminalScanLimit: config.DownlinkTerminal.ScanLimit,
		downlinkRetention: downlink.RetentionPolicy{
			DeliveredTTL: config.DownlinkRetention.DeliveredTTL,
			FailedTTL:    config.DownlinkRetention.FailedTTL,
			DiscardedTTL: config.DownlinkRetention.DiscardedTTL,
			Limit:        config.DownlinkRetention.CleanupLimit,
		},
		downlinkCleanupInterval: config.DownlinkRetention.CleanupInterval,
		clusterRouteRefresher:   newClusterRouteRefresher(config, clusterRegistry, config.Sessions, logger),
	}

	zServer.SetOnConnStart(gateway.onConnStart)
	zServer.SetOnConnStop(gateway.onConnStop)

	router := NewIngressRouter(
		logger,
		zServer.GetConnMgr(),
		newIngressPipeline(config, logger, clusterRegistry, trafficPolicyHandler),
		upstream,
		downlinkService,
		config.DownlinkDelivery.BindFlushLimit,
	)

	for _, msgID := range msgIDs {
		zServer.AddRouter(msgID, router)
	}
	logger.Info(
		"registered zinx routes",
		zap.Int("count", len(msgIDs)),
		zap.Strings("msg_id_ranges", compactMsgIDRanges(msgIDs)),
	)

	return gateway, nil
}

func newIngressPipeline(
	config Config,
	logger *zap.Logger,
	registry cluster.OnlineRegistry,
	trafficPolicyHandler pipeline.Handler,
) *pipeline.Chain {
	return pipeline.NewChain(
		pipeline.NewAuthHandler(config.Verifier, logger),
		pipeline.NewPolicyHandler(config.Pipeline.Policy),
		pipeline.NewRateLimitHandler(config.Pipeline.RateLimit),
		trafficPolicyHandler,
		pipeline.NewSessionBindHandler(config.Sessions, config.GatewayNode, sessionIDProperty, logger),
		newClusterBindHandler(config, registry, logger),
		pipeline.NewAccessLogHandler(logger),
	)
}

func (g *Gateway) Serve() {
	g.Start()
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signalCh
	signal.Stop(signalCh)
	g.logger.Info("gateway received shutdown signal", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := g.Shutdown(ctx); err != nil {
		g.logger.Warn("gateway graceful shutdown completed with errors", zap.Error(err))
	}
}

func (g *Gateway) Start() {
	if g.started.Swap(true) {
		return
	}

	g.runtime.MarkStarted(time.Now())
	metrics.SetGatewayReadiness("ready")
	g.startInternalHTTP()
	g.startDownlinkRetryWorker()
	g.startDownlinkTerminalWorker()
	g.startDownlinkCleanupWorker()
	g.startClusterRouteRefresher()

	g.server.Start()
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	g.shutdownOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		g.logger.Info("starting gateway graceful shutdown")
		g.health.BeginDrain()
		metrics.SetGatewayReadiness("draining")

		g.shutdownClusterRouteRefresher()
		g.shutdownZinxServer()
		g.shutdownTrafficPolicy()
		g.shutdownAuthVerifier()
		unbound := g.unbindAllClusterRoutes(ctx)
		g.shutdownInternalHTTPWithContext(ctx)
		g.shutdownAdminSessions()
		g.shutdownAdminAudit()
		g.shutdownDownlinkCleanupWorker()
		g.shutdownDownlinkTerminalWorker()
		g.shutdownDownlinkRetryWorker()
		g.shutdownUpstream()
		g.shutdownDownlink()
		g.shutdownClusterRegistry()
		metrics.SetSessionsOnline(g.sessions.Len())
		metrics.SetClientsOnline(g.sessions.UniqueClientLen())

		g.logger.Info("gateway graceful shutdown completed", zap.Int("cluster_routes_unbound", unbound))
	})

	return g.shutdownErr
}

func (g *Gateway) startClusterRouteRefresher() {
	if g.clusterRouteRefresher == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.clusterRefreshCancel = cancel
	g.clusterRefreshCompleted = make(chan struct{})

	g.logger.Info(
		"starting cluster route refresher",
		zap.Duration("interval", g.clusterRouteRefresher.interval),
		zap.Duration("timeout", g.clusterRouteRefresher.timeout),
	)

	go func() {
		defer close(g.clusterRefreshCompleted)
		g.clusterRouteRefresher.run(ctx)
	}()
}

func (g *Gateway) shutdownClusterRouteRefresher() {
	if g.clusterRefreshCancel == nil {
		return
	}

	g.clusterRefreshCancel()
	if g.clusterRefreshCompleted != nil {
		<-g.clusterRefreshCompleted
	}
}

func (g *Gateway) SessionManager() *session.Manager {
	return g.sessions
}

func (g *Gateway) startInternalHTTP() {
	if g.internalHTTP == nil {
		return
	}

	g.logger.Info("starting internal HTTP API", zap.String("addr", g.internalHTTP.Addr))

	go func() {
		if err := g.internalHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.logger.Error("internal HTTP API stopped unexpectedly", zap.Error(err))
		}
	}()
}

func (g *Gateway) shutdownInternalHTTP() {
	if g.internalHTTP == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := g.internalHTTP.Shutdown(ctx); err != nil {
		g.logger.Warn("failed to shutdown internal HTTP API cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownInternalHTTPWithContext(ctx context.Context) {
	if g.internalHTTP == nil {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := g.internalHTTP.Shutdown(ctx); err != nil {
		g.shutdownErr = errors.Join(g.shutdownErr, err)
		g.logger.Warn("failed to shutdown internal HTTP API cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownZinxServer() {
	if g.server == nil || !g.started.Load() {
		return
	}

	g.server.Stop()
}

func (g *Gateway) shutdownUpstream() {
	if g.upstream == nil {
		return
	}

	if err := g.upstream.Close(); err != nil {
		g.logger.Warn("failed to shutdown upstream routes cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownAuthVerifier() {
	if g.authVerifierCloser == nil {
		return
	}
	if err := g.authVerifierCloser.Close(); err != nil {
		g.shutdownErr = errors.Join(g.shutdownErr, err)
		g.logger.Warn("failed to shutdown authentication verifier cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownTrafficPolicy() {
	if g.trafficPolicyCloser == nil {
		return
	}
	if err := g.trafficPolicyCloser.Close(); err != nil {
		g.shutdownErr = errors.Join(g.shutdownErr, err)
		g.logger.Warn("failed to shutdown traffic policy quota store cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownAdminAudit() {
	if g.adminAuditCloser == nil {
		return
	}
	if err := g.adminAuditCloser.Close(); err != nil {
		g.shutdownErr = errors.Join(g.shutdownErr, err)
		g.logger.Warn("failed to shutdown admin audit store cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownAdminSessions() {
	if g.adminSessionCloser == nil {
		return
	}
	if err := g.adminSessionCloser.Close(); err != nil {
		g.shutdownErr = errors.Join(g.shutdownErr, err)
		g.logger.Warn("failed to shutdown admin session store cleanly", zap.Error(err))
	}
}

func closeWithLog(closer io.Closer, logger *zap.Logger, resource string) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		logger.Warn("failed to close resource after gateway construction error", zap.String("resource", resource), zap.Error(err))
	}
}

func (g *Gateway) shutdownDownlink() {
	if g.downlinkCloser == nil {
		return
	}

	if err := g.downlinkCloser.Close(); err != nil {
		g.logger.Warn("failed to shutdown downlink store cleanly", zap.Error(err))
	}
}

func (g *Gateway) shutdownClusterRegistry() {
	if g.clusterRegistryCloser == nil {
		return
	}

	if err := g.clusterRegistryCloser.Close(); err != nil {
		g.logger.Warn("failed to shutdown cluster registry cleanly", zap.Error(err))
	}
}

func (g *Gateway) startDownlinkRetryWorker() {
	if g.downlink == nil || !g.downlink.HasStore() {
		return
	}

	interval := g.downlinkRetryInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.downlinkWorkerCancel = cancel
	g.downlinkWorkerCompleted = make(chan struct{})

	g.logger.Info(
		"starting downlink retry worker",
		zap.Duration("interval", interval),
		zap.Int("scan_limit", g.downlinkRetryScanLimit),
		zap.Bool("fairness_enabled", g.downlinkRetryFairness.Enabled),
		zap.Int("fairness_candidate_multiplier", g.downlinkRetryFairness.CandidateMultiplier),
	)

	go func() {
		defer close(g.downlinkWorkerCompleted)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.retryDownlinkDue(ctx)
			}
		}
	}()
}

func (g *Gateway) retryDownlinkDue(ctx context.Context) {
	startedAt := time.Now()
	result, err := g.downlink.RetryDue(ctx, g.downlinkRetryScanLimit)
	if err != nil {
		g.logger.Warn("downlink retry worker failed", zap.Error(err))
		return
	}
	if result.Scanned == 0 {
		return
	}

	g.logger.Info(
		"downlink retry worker completed",
		zap.Int("scanned", result.Scanned),
		zap.Int("sent", result.Sent),
		zap.Int("queued", result.Queued),
		zap.Int("failed", result.Failed),
		zap.String("selection_mode", result.SelectionMode),
		zap.Int("selected_devices", result.SelectedDevices),
		zap.Int("max_per_device", result.MaxPerDevice),
		zap.Duration("duration", time.Since(startedAt)),
	)
}

func (g *Gateway) shutdownDownlinkRetryWorker() {
	if g.downlinkWorkerCancel == nil {
		return
	}

	g.downlinkWorkerCancel()
	if g.downlinkWorkerCompleted != nil {
		<-g.downlinkWorkerCompleted
	}
}

func (g *Gateway) startDownlinkTerminalWorker() {
	if g.downlink == nil || !g.downlink.HasTerminalPublisher() {
		return
	}
	interval := g.downlinkTerminalInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.downlinkTerminalCancel = cancel
	g.downlinkTerminalCompleted = make(chan struct{})
	g.logger.Info(
		"starting downlink terminal publisher worker",
		zap.Duration("interval", interval),
		zap.Int("scan_limit", g.downlinkTerminalScanLimit),
	)

	go func() {
		defer close(g.downlinkTerminalCompleted)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.publishDownlinkTerminalDue(ctx)
			}
		}
	}()
}

func (g *Gateway) publishDownlinkTerminalDue(ctx context.Context) {
	result, err := g.downlink.PublishTerminalDue(ctx, g.downlinkTerminalScanLimit)
	if err != nil {
		g.logger.Warn("downlink terminal publisher worker failed", zap.Error(err))
		return
	}
	if result.Scanned == 0 {
		return
	}
	g.logger.Info(
		"downlink terminal publisher worker completed",
		zap.Int("scanned", result.Scanned),
		zap.Int("published", result.Published),
		zap.Int("failed", result.Failed),
	)
}

func (g *Gateway) shutdownDownlinkTerminalWorker() {
	if g.downlinkTerminalCancel == nil {
		return
	}
	g.downlinkTerminalCancel()
	if g.downlinkTerminalCompleted != nil {
		<-g.downlinkTerminalCompleted
	}
}

func (g *Gateway) startDownlinkCleanupWorker() {
	if g.downlink == nil || !g.downlink.HasStore() || !retentionPolicyEnabled(g.downlinkRetention) {
		return
	}

	interval := g.downlinkCleanupInterval
	if interval <= 0 {
		interval = time.Hour
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.downlinkCleanupCancel = cancel
	g.downlinkCleanupCompleted = make(chan struct{})

	g.logger.Info(
		"starting downlink cleanup worker",
		zap.Duration("interval", interval),
		zap.Duration("delivered_ttl", g.downlinkRetention.DeliveredTTL),
		zap.Duration("failed_ttl", g.downlinkRetention.FailedTTL),
		zap.Duration("discarded_ttl", g.downlinkRetention.DiscardedTTL),
		zap.Int("cleanup_limit", g.downlinkRetention.Limit),
	)

	go func() {
		defer close(g.downlinkCleanupCompleted)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.cleanupDownlinkExpired(ctx)
			}
		}
	}()
}

func (g *Gateway) cleanupDownlinkExpired(ctx context.Context) {
	startedAt := time.Now()
	result, err := g.downlink.CleanupExpired(ctx, g.downlinkRetention)
	if err != nil {
		g.logger.Warn("downlink cleanup worker failed", zap.Error(err))
		return
	}
	if result.Total() == 0 {
		return
	}

	g.logger.Info(
		"downlink cleanup worker completed",
		zap.Int("delivered", result.Delivered),
		zap.Int("failed", result.Failed),
		zap.Int("discarded", result.Discarded),
		zap.Duration("duration", time.Since(startedAt)),
	)
}

func (g *Gateway) shutdownDownlinkCleanupWorker() {
	if g.downlinkCleanupCancel == nil {
		return
	}

	g.downlinkCleanupCancel()
	if g.downlinkCleanupCompleted != nil {
		<-g.downlinkCleanupCompleted
	}
}

func retentionPolicyEnabled(policy downlink.RetentionPolicy) bool {
	return policy.DeliveredTTL > 0 || policy.FailedTTL > 0 || policy.DiscardedTTL > 0
}

func registeredMsgIDs(config Config) ([]uint32, error) {
	seen := make(map[uint32]struct{})
	seen[protocol.MsgIDBind] = struct{}{}
	seen[protocol.MsgIDDownlinkAck] = struct{}{}
	for _, msgID := range config.RouteMsgIDs {
		seen[msgID] = struct{}{}
	}

	for _, route := range config.UpstreamRoutes {
		msgIDMax := route.MsgIDMax
		if msgIDMax == 0 {
			msgIDMax = route.MsgIDMin
		}
		if route.MsgIDMin == 0 || msgIDMax < route.MsgIDMin {
			return nil, fmt.Errorf("upstream route %q has invalid msg id range %d-%d", route.Name, route.MsgIDMin, route.MsgIDMax)
		}
		if msgIDMax-route.MsgIDMin > 10000 {
			return nil, fmt.Errorf("upstream route %q msg id range is too large: %d-%d", route.Name, route.MsgIDMin, msgIDMax)
		}

		for msgID := route.MsgIDMin; ; msgID++ {
			seen[msgID] = struct{}{}
			if msgID == msgIDMax {
				break
			}
		}
	}
	if config.UpstreamRoutesFile.Reload.Enabled {
		if len(config.UpstreamRoutesFile.Reload.AcceptedMsgIDRanges) == 0 {
			return nil, fmt.Errorf("upstream route reload requires accepted msg id ranges")
		}
		for index, accepted := range config.UpstreamRoutesFile.Reload.AcceptedMsgIDRanges {
			msgIDMax := accepted.Max
			if msgIDMax == 0 {
				msgIDMax = accepted.Min
			}
			if accepted.Min == 0 || msgIDMax < accepted.Min {
				return nil, fmt.Errorf(
					"upstream route reload accepted msg id range #%d is invalid: %d-%d",
					index+1,
					accepted.Min,
					accepted.Max,
				)
			}
			if msgIDMax-accepted.Min > 10000 {
				return nil, fmt.Errorf(
					"upstream route reload accepted msg id range #%d is too large: %d-%d",
					index+1,
					accepted.Min,
					msgIDMax,
				)
			}
			for msgID := accepted.Min; ; msgID++ {
				seen[msgID] = struct{}{}
				if msgID == msgIDMax {
					break
				}
			}
		}
	}

	msgIDs := make([]uint32, 0, len(seen))
	for msgID := range seen {
		msgIDs = append(msgIDs, msgID)
	}
	sort.Slice(msgIDs, func(i, j int) bool {
		return msgIDs[i] < msgIDs[j]
	})

	return msgIDs, nil
}

func compactMsgIDRanges(msgIDs []uint32) []string {
	if len(msgIDs) == 0 {
		return nil
	}

	ranges := make([]string, 0, len(msgIDs))
	start := msgIDs[0]
	previous := msgIDs[0]
	for _, msgID := range msgIDs[1:] {
		if msgID == previous+1 {
			previous = msgID
			continue
		}

		ranges = append(ranges, formatMsgIDRange(start, previous))
		start = msgID
		previous = msgID
	}

	ranges = append(ranges, formatMsgIDRange(start, previous))
	return ranges
}

func formatMsgIDRange(start, end uint32) string {
	if start == end {
		return strconv.FormatUint(uint64(start), 10)
	}

	return strconv.FormatUint(uint64(start), 10) + "-" + strconv.FormatUint(uint64(end), 10)
}

func (g *Gateway) onConnStart(conn ziface.IConnection) {
	g.logger.Info(
		"connection opened",
		zap.Uint64("conn_id", conn.GetConnID()),
		zap.String("remote_addr", conn.RemoteAddrString()),
	)
}

func (g *Gateway) onConnStop(conn ziface.IConnection) {
	removed, ok := g.sessions.UnbindByConnID(conn.GetConnID())
	metrics.SetSessionsOnline(g.sessions.Len())
	metrics.SetClientsOnline(g.sessions.UniqueClientLen())
	conn.RemoveProperty(sessionIDProperty)

	if !ok {
		g.logger.Info(
			"connection closed",
			zap.Uint64("conn_id", conn.GetConnID()),
			zap.String("remote_addr", conn.RemoteAddrString()),
		)
		return
	}

	unbindClusterRoute(context.Background(), g.clusterRegistry, removed, g.logger)
	g.logger.Info(
		"session unbound on connection close",
		zap.Uint64("conn_id", conn.GetConnID()),
		zap.String("session_id", removed.SessionID),
		zap.String("client_id", removed.ClientID),
		zap.String("device_id", removed.DeviceID),
	)
}
