package pipeline

import (
	"context"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type Context struct {
	BaseContext context.Context
	Request     ziface.IRequest
	Packet      *protocol.Packet
	Logger      *zap.Logger

	Principal  *auth.Principal
	BindResult *session.BindResult
	Session    *session.Session

	RouteResolutionSet bool
	RouteFound         bool
	RouteName          string
}

func NewContext(request ziface.IRequest, packet *protocol.Packet, logger *zap.Logger) *Context {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Context{
		BaseContext: requestContext(request),
		Request:     request,
		Packet:      packet,
		Logger:      logger,
	}
}

func (c *Context) Context() context.Context {
	if c == nil || c.BaseContext == nil {
		return context.Background()
	}

	return c.BaseContext
}

func (c *Context) Conn() ziface.IConnection {
	if c == nil || c.Request == nil {
		return nil
	}

	return c.Request.GetConnection()
}

func (c *Context) ConnID() uint64 {
	conn := c.Conn()
	if conn == nil {
		return 0
	}

	return conn.GetConnID()
}

func (c *Context) SetRouteResolution(routeName string, found bool) {
	if c == nil {
		return
	}
	c.RouteResolutionSet = true
	c.RouteFound = found
	c.RouteName = routeName
}

func requestContext(request ziface.IRequest) context.Context {
	if request == nil || request.GetConnection() == nil || request.GetConnection().Context() == nil {
		return context.Background()
	}

	return request.GetConnection().Context()
}
