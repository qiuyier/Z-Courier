package cluster

import (
	"context"
	"errors"
	"sort"
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

type RouteLister interface {
	List(ctx context.Context, filter RouteListFilter) (RouteListResult, error)
}

type RouteListFilter struct {
	SessionID string
	ClientID  string
	DeviceID  string
	Limit     int
}

type RouteListResult struct {
	Total         int
	UniqueClients int
	Routes        []RouteEntry
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

func routeEntryMatchesFilter(entry RouteEntry, filter RouteListFilter) bool {
	if filter.SessionID != "" && entry.SessionID != filter.SessionID {
		return false
	}
	if filter.ClientID != "" && entry.ClientID != filter.ClientID {
		return false
	}
	if filter.DeviceID != "" && entry.DeviceID != filter.DeviceID {
		return false
	}
	return true
}

func sortRouteEntries(entries []RouteEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i]
		right := entries[j]
		if left.ClientID != right.ClientID {
			return left.ClientID < right.ClientID
		}
		if left.DeviceID != right.DeviceID {
			return left.DeviceID < right.DeviceID
		}
		if left.GatewayNode != right.GatewayNode {
			return left.GatewayNode < right.GatewayNode
		}
		return left.SessionID < right.SessionID
	})
}

func uniqueRouteClientCount(entries []RouteEntry) int {
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.ClientID == "" {
			continue
		}
		seen[entry.ClientID] = struct{}{}
	}
	return len(seen)
}
