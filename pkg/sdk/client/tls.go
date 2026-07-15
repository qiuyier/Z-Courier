package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	"github.com/qiuyier/Z-Courier/internal/tlsconfig"
)

// TLSConfig enables server-authenticated TLS for client connections.
// CAFile is optional and adds private PEM roots to the system root pool.
// ServerName defaults to the host in Config.Address.
type TLSConfig struct {
	// CAFile optionally adds PEM certificates to the system root pool.
	CAFile string
	// ServerName overrides the certificate identity derived from Config.Address.
	ServerName string
}

type tlsDialer struct {
	next   Dialer
	config *tls.Config
}

func (d *tlsDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	connection, err := d.next.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tlsConnection := tls.Client(connection, d.config.Clone())
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	return tlsConnection, nil
}

func normalizeTLSConfig(config *TLSConfig, address string) (*tls.Config, error) {
	if config == nil {
		return nil, nil
	}
	serverName := strings.TrimSpace(config.ServerName)
	if serverName == "" {
		host, _, err := net.SplitHostPort(strings.TrimSpace(address))
		if err != nil || strings.TrimSpace(host) == "" {
			return nil, fmt.Errorf("%w: TLS server name is required when address is not host:port", ErrInvalidConfig)
		}
		serverName = host
	}
	tlsConfig, err := tlsconfig.Build(tlsconfig.Files{
		CAFile:     config.CAFile,
		ServerName: serverName,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: TLS: %w", ErrInvalidConfig, err)
	}
	return tlsConfig, nil
}
