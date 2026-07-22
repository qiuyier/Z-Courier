// Command e2etlsproxy runs the ephemeral TLS edge used by SDK integration tests.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type config struct {
	ListenAddress    string
	UpstreamAddress  string
	CAFile           string
	HandshakeTimeout time.Duration
	DialTimeout      time.Duration
}

func main() {
	configuration := parseConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, configuration); err != nil {
		fmt.Fprintf(os.Stderr, "e2e TLS proxy failed: %v\n", err)
		os.Exit(1)
	}
}

func parseConfig() config {
	var configuration config
	flag.StringVar(&configuration.ListenAddress, "listen", "127.0.0.1:9900", "TLS listen address")
	flag.StringVar(&configuration.UpstreamAddress, "upstream", "127.0.0.1:9899", "plaintext upstream address")
	flag.StringVar(&configuration.CAFile, "ca-file", "", "path that receives the ephemeral CA certificate")
	flag.DurationVar(&configuration.HandshakeTimeout, "handshake-timeout", 5*time.Second, "TLS handshake timeout")
	flag.DurationVar(&configuration.DialTimeout, "dial-timeout", 5*time.Second, "upstream dial timeout")
	flag.Parse()
	return configuration
}

func run(ctx context.Context, configuration config) error {
	if configuration.CAFile == "" {
		return errors.New("-ca-file is required")
	}
	if configuration.HandshakeTimeout <= 0 || configuration.DialTimeout <= 0 {
		return errors.New("timeouts must be greater than zero")
	}

	certificate, caPEM, err := generateCertificate(time.Now())
	if err != nil {
		return err
	}
	rawListener, err := net.Listen("tcp", configuration.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", configuration.ListenAddress, err)
	}
	defer rawListener.Close()
	if err := os.WriteFile(configuration.CAFile, caPEM, 0o600); err != nil {
		return fmt.Errorf("write CA file: %w", err)
	}

	listener := tls.NewListener(rawListener, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	})
	fmt.Printf("e2e TLS proxy ready: listen=%s upstream=%s\n", rawListener.Addr(), configuration.UpstreamAddress)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	var connections sync.WaitGroup
	defer connections.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept TLS connection: %w", err)
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			proxyConnection(ctx, connection, configuration)
		}()
	}
}

func proxyConnection(ctx context.Context, connection net.Conn, configuration config) {
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return
	}
	if err := tlsConnection.SetDeadline(time.Now().Add(configuration.HandshakeTimeout)); err != nil {
		return
	}
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		return
	}
	if err := tlsConnection.SetDeadline(time.Time{}); err != nil {
		return
	}

	dialer := &net.Dialer{Timeout: configuration.DialTimeout}
	upstream, err := dialer.DialContext(ctx, "tcp", configuration.UpstreamAddress)
	if err != nil {
		return
	}
	defer upstream.Close()
	stopOnShutdown := context.AfterFunc(ctx, func() {
		_ = upstream.Close()
		_ = tlsConnection.Close()
	})
	defer stopOnShutdown()

	copied := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, tlsConnection)
		copied <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(tlsConnection, upstream)
		copied <- struct{}{}
	}()
	<-copied
	_ = upstream.Close()
	_ = tlsConnection.Close()
	<-copied
}

func generateCertificate(now time.Time) (tls.Certificate, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("generate CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Z-Courier E2E TLS CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse CA certificate: %w", err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("generate server key: %w", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Z-Courier E2E TLS Edge"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(
		rand.Reader,
		serverTemplate,
		caCertificate,
		&serverKey.PublicKey,
		caKey,
	)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("create server certificate: %w", err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("marshal server key: %w", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}),
	)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load server key pair: %w", err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
