package server

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/adminaudit"
)

func newAdminAuditStore(config Config) (adminaudit.Trail, io.Closer, error) {
	if config.AdminAudit != nil {
		closer, _ := config.AdminAudit.(io.Closer)
		return config.AdminAudit, closer, nil
	}
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" {
		return nil, nil, nil
	}

	switch strings.ToLower(strings.TrimSpace(config.AdminAuditStorage.Type)) {
	case "", "memory":
		return adminaudit.NewStore(adminaudit.StoreConfig{Capacity: config.AdminAuditStorage.Capacity}), nil, nil
	case "postgres":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		store, err := adminaudit.NewPostgresStore(ctx, adminaudit.PostgresStoreConfig{
			DSN:              config.AdminAuditStorage.Postgres.DSN,
			AutoMigrate:      config.AdminAuditStorage.Postgres.AutoMigrate,
			MaxOpenConns:     config.AdminAuditStorage.Postgres.MaxOpenConns,
			MaxIdleConns:     config.AdminAuditStorage.Postgres.MaxIdleConns,
			ConnMaxLifetime:  config.AdminAuditStorage.Postgres.ConnMaxLifetime,
			OperationTimeout: config.AdminAuditStorage.Postgres.OperationTimeout,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("admin audit postgres store: %w", err)
		}
		return store, store, nil
	default:
		return nil, nil, fmt.Errorf("unsupported admin audit storage type %q", config.AdminAuditStorage.Type)
	}
}
