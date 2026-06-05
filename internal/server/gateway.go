package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
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

	internalHTTP            *http.Server
	upstream                *router.Engine
	downlink                *downlink.Service
	downlinkCloser          io.Closer
	downlinkRetryInterval   time.Duration
	downlinkRetryScanLimit  int
	downlinkWorkerCancel    context.CancelFunc
	downlinkWorkerCompleted chan struct{}
}

func New(config Config, logger *zap.Logger) (*Gateway, error) {
	if logger == nil {
		logger = defaultLogger()
	}

	config = normalizeConfig(config)
	msgIDs, err := registeredMsgIDs(config)
	if err != nil {
		return nil, err
	}
	upstream, err :=
		newUpstreamEngine(config)
	if err != nil {
		return nil, err
	}
	zServer := znet.NewServer()
	downlinkService, downlinkCloser, err := newDownlinkService(config, zServer.GetConnMgr())
	if err != nil {
		if upstream != nil {
			_ = upstream.Close()
		}
		return nil, err
	}

	gateway := &Gateway{
		server:                 zServer,
		sessions:               config.Sessions,
		logger:                 logger,
		internalHTTP:           newInternalHTTPServer(config, logger, downlinkService),
		upstream:               upstream,
		downlink:               downlinkService,
		downlinkCloser:         downlinkCloser,
		downlinkRetryInterval:  config.DownlinkDelivery.RetryInterval,
		downlinkRetryScanLimit: config.DownlinkDelivery.ScanLimit,
	}

	zServer.SetOnConnStart(gateway.onConnStart)
	zServer.SetOnConnStop(gateway.onConnStop)

	router := NewIngressRouter(
		logger,
		zServer.GetConnMgr(),
		newIngressPipeline(config, logger),
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

func newIngressPipeline(config Config, logger *zap.Logger) *pipeline.Chain {
	return pipeline.NewChain(
		pipeline.NewAuthHandler(config.Verifier, logger),
		pipeline.NewPolicyHandler(config.Pipeline.Policy),
		pipeline.NewRateLimitHandler(config.Pipeline.RateLimit),
		pipeline.NewSessionBindHandler(config.Sessions, config.GatewayNode, sessionIDProperty, logger),
		pipeline.NewAccessLogHandler(logger),
	)
}

func (g *Gateway) Serve() {
	g.startInternalHTTP()
	g.startDownlinkRetryWorker()
	defer g.shutdownDownlink()
	defer g.shutdownUpstream()
	defer g.shutdownDownlinkRetryWorker()
	defer g.shutdownInternalHTTP()

	g.server.Serve()
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

func (g *Gateway) shutdownUpstream() {
	if g.upstream == nil {
		return
	}

	if err := g.upstream.Close(); err != nil {
		g.logger.Warn("failed to shutdown upstream routes cleanly", zap.Error(err))
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

func registeredMsgIDs(config Config) ([]uint32, error) {
	seen := make(map[uint32]struct{})
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
	conn.RemoveProperty(sessionIDProperty)

	if !ok {
		g.logger.Info(
			"connection closed",
			zap.Uint64("conn_id", conn.GetConnID()),
			zap.String("remote_addr", conn.RemoteAddrString()),
		)
		return
	}

	g.logger.Info(
		"session unbound on connection close",
		zap.Uint64("conn_id", conn.GetConnID()),
		zap.String("session_id", removed.SessionID),
		zap.String("client_id", removed.ClientID),
		zap.String("device_id", removed.DeviceID),
	)
}
