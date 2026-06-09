package cluster

import (
	"context"
	"errors"
	"time"
)

var (
	ErrClosed            = errors.New("cluster: registry is closed")
	ErrInvalidRouteKey   = errors.New("cluster: invalid route key")
	ErrInvalidRouteEntry = errors.New("cluster: invalid route entry")
	ErrRouteNotFound     = errors.New("cluster: route not found")
	ErrSessionMismatch   = errors.New("cluster: session mismatch")
)

type OnlineRegistry interface {
	Bind(ctx context.Context, entry RouteEntry) error
	Unbind(ctx context.Context, key RouteKey, sessionID string) error
	Lookup(ctx context.Context, key RouteKey) (RouteEntry, bool, error)
	Touch(ctx context.Context, entry RouteEntry) error
	Close() error
}

type RouteKey struct {
	ClientID string
	DeviceID string
}

type RouteEntry struct {
	ClientID     string
	DeviceID     string
	SessionID    string
	GatewayNode  string
	InternalAddr string
	TokenID      string
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

func (e RouteEntry) Key() RouteKey {
	return RouteKey{
		ClientID: e.ClientID,
		DeviceID: e.DeviceID,
	}
}

func (e RouteEntry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt)
}

func validateRouteKey(key RouteKey) error {
	if key.ClientID == "" || key.DeviceID == "" {
		return ErrInvalidRouteKey
	}
	return nil
}

func validateRouteEntry(entry RouteEntry) error {
	if err := validateRouteKey(entry.Key()); err != nil {
		return err
	}
	if entry.SessionID == "" || entry.GatewayNode == "" {
		return ErrInvalidRouteEntry
	}
	return nil
}
