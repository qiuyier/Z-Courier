package server

import (
	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type Gateway struct {
	server   ziface.IServer
	sessions *session.Manager
	logger   *zap.Logger
}

func New(config Config, logger *zap.Logger) *Gateway {
	if logger == nil {
		logger = defaultLogger()
	}

	config = normalizeConfig(config)

	zServer := znet.NewServer()
	gateway := &Gateway{
		server:   zServer,
		sessions: config.Sessions,
		logger:   logger,
	}

	zServer.SetOnConnStart(gateway.onConnStart)
	zServer.SetOnConnStop(gateway.onConnStop)

	router := NewIngressRouter(
		logger,
		config.Verifier,
		config.Sessions,
		zServer.GetConnMgr(),
		config.GatewayNode,
	)

	for _, msgID := range config.RouteMsgIDs {
		zServer.AddRouter(msgID, router)
	}

	return gateway
}

func (g *Gateway) Serve() {
	g.server.Serve()
}

func (g *Gateway) SessionManager() *session.Manager {
	return g.sessions
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
