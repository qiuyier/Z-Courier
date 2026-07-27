package router

import (
	"context"
	"errors"

	"github.com/qiuyier/Z-Courier/internal/protocol"
)

type Forwarder interface {
	Forward(ctx context.Context, packet *protocol.Packet) (*ForwardResult, error)
}

type closeForwarder interface {
	Close() error
}

type ForwardResult struct {
	RouteName   string
	TargetType  string
	Status      string
	StatusCode  int
	Endpoint    string
	Attempts    int
	MaxAttempts int
}

type Route struct {
	Name      string
	MsgIDMin  uint32
	MsgIDMax  uint32
	Forwarder Forwarder
}

func (r Route) Matches(msgID uint32) bool {
	if r.MsgIDMin == 0 && r.MsgIDMax == 0 {
		return false
	}
	if r.MsgIDMax == 0 {
		return msgID == r.MsgIDMin
	}

	return msgID >= r.MsgIDMin && msgID <= r.MsgIDMax
}

type Engine struct {
	routes []Route
}

func NewEngine(routes []Route) *Engine {
	copied := make([]Route, len(routes))
	copy(copied, routes)

	return &Engine{routes: copied}
}

func (e *Engine) Forward(ctx context.Context, packet *protocol.Packet) (*ForwardResult, error) {
	if e == nil || packet == nil {
		return nil, ErrRouteNotFound
	}

	for _, route := range e.routes {
		if !route.Matches(packet.MsgID) || route.Forwarder == nil {
			continue
		}

		result, err := route.Forwarder.Forward(ctx, packet)
		if result == nil {
			result = &ForwardResult{}
		}
		result.RouteName = route.Name
		return result, err
	}

	return nil, ErrRouteNotFound
}

func (e *Engine) Close() error {
	if e == nil {
		return nil
	}

	var joined error
	for _, route := range e.routes {
		closer, ok := route.Forwarder.(closeForwarder)
		if !ok {
			continue
		}

		joined = errors.Join(joined, closer.Close())
	}

	return joined
}
