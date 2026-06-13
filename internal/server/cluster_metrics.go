package server

import (
	"context"
	"errors"

	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/metrics"
)

type metricsRegistry struct {
	inner cluster.OnlineRegistry
}

func newMetricsRegistry(inner cluster.OnlineRegistry) cluster.OnlineRegistry {
	if inner == nil {
		return nil
	}

	return &metricsRegistry{inner: inner}
}

func (r *metricsRegistry) Bind(ctx context.Context, entry cluster.RouteEntry) error {
	err := r.inner.Bind(ctx, entry)
	metrics.RecordClusterRegistryBind(resultFromError(err))
	return err
}

func (r *metricsRegistry) Unbind(ctx context.Context, key cluster.RouteKey, sessionID string) error {
	err := r.inner.Unbind(ctx, key, sessionID)
	metrics.RecordClusterRegistryUnbind(resultFromError(err))
	return err
}

func (r *metricsRegistry) Lookup(ctx context.Context, key cluster.RouteKey) (cluster.RouteEntry, bool, error) {
	entry, ok, err := r.inner.Lookup(ctx, key)
	switch {
	case err != nil:
		metrics.RecordClusterRegistryLookup("failure")
	case ok:
		metrics.RecordClusterRegistryLookup("hit")
	default:
		metrics.RecordClusterRegistryLookup("miss")
	}
	return entry, ok, err
}

func (r *metricsRegistry) Touch(ctx context.Context, entry cluster.RouteEntry) error {
	err := r.inner.Touch(ctx, entry)
	metrics.RecordClusterRegistryTouch(clusterTouchResult(err))
	return err
}

func (r *metricsRegistry) Close() error {
	return r.inner.Close()
}

func resultFromError(err error) string {
	if err != nil {
		return "failure"
	}
	return "success"
}

func clusterTouchResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, cluster.ErrRouteNotFound):
		return "miss"
	case errors.Is(err, cluster.ErrSessionMismatch):
		return "session_mismatch"
	default:
		return "failure"
	}
}
