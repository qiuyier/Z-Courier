package client

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
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestClientConnectsWithPrivateCA(t *testing.T) {
	pki := newClientTestPKI(t)
	listener, serverDone := startSingleTLSBindServer(t, pki)
	defer listener.Close()
	var dialedNetwork string
	var dialedAddress string

	client, err := New(Config{
		Address:  listener.Addr().String(),
		ClientID: "claimed-client",
		DeviceID: "device-1",
		Token:    "token-1",
		TLS:      &TLSConfig{CAFile: pki.caFile},
		Dialer: dialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialedNetwork = network
			dialedAddress = address
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if !client.Ready() || client.Binding().SessionID != "session-1" {
		t.Fatalf("client ready=%t binding=%+v", client.Ready(), client.Binding())
	}
	if dialedNetwork != "tcp" || dialedAddress != listener.Addr().String() {
		t.Fatalf("wrapped Dialer received network=%q address=%q", dialedNetwork, dialedAddress)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("TLS bind server error = %v", err)
	}
}

func TestClientTLSDefaultsToAddressAndSystemRoots(t *testing.T) {
	config, err := normalizeTLSConfig(&TLSConfig{}, "gateway.example.internal:8999")
	if err != nil {
		t.Fatalf("normalizeTLSConfig() error = %v", err)
	}
	if config.ServerName != "gateway.example.internal" {
		t.Fatalf("ServerName = %q", config.ServerName)
	}
	if config.RootCAs != nil {
		t.Fatal("RootCAs is non-nil, want the Go system root pool")
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2", config.MinVersion)
	}
}

func TestClientTLSHandshakeTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		var buffer [1024]byte
		for {
			if _, readErr := connection.Read(buffer[:]); readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, net.ErrClosed) {
					serverDone <- nil
					return
				}
				serverDone <- readErr
				return
			}
		}
	}()

	client, err := New(Config{
		Address:        listener.Addr().String(),
		ClientID:       "client-1",
		DeviceID:       "device-1",
		Token:          "token-1",
		TLS:            &TLSConfig{},
		ConnectTimeout: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()
	if err := client.Connect(context.Background()); !errors.Is(err, ErrConnectTimeout) {
		t.Fatalf("Connect() error = %v, want ErrConnectTimeout", err)
	}
	if err := <-serverDone; err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("stalled TLS server error = %v", err)
	}
}

func TestClientRejectsInvalidTLSIdentity(t *testing.T) {
	t.Run("unknown authority", func(t *testing.T) {
		pki := newClientTestPKI(t)
		listener, serverDone := startSingleTLSBindServer(t, pki)
		defer listener.Close()

		client, err := New(Config{
			Address:  listener.Addr().String(),
			ClientID: "claimed-client",
			DeviceID: "device-1",
			Token:    "token-1",
			TLS:      &TLSConfig{},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer client.Close()
		err = client.Connect(context.Background())
		if err == nil || !strings.Contains(err.Error(), "certificate") ||
			(!strings.Contains(err.Error(), "not trusted") && !strings.Contains(err.Error(), "unknown authority")) {
			t.Fatalf("Connect() error = %v, want an untrusted certificate error", err)
		}
		<-serverDone
	})

	t.Run("server name mismatch", func(t *testing.T) {
		pki := newClientTestPKI(t)
		listener, serverDone := startSingleTLSBindServer(t, pki)
		defer listener.Close()

		client, err := New(Config{
			Address:  listener.Addr().String(),
			ClientID: "claimed-client",
			DeviceID: "device-1",
			Token:    "token-1",
			TLS: &TLSConfig{
				CAFile:     pki.caFile,
				ServerName: "wrong.gateway.local",
			},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer client.Close()
		err = client.Connect(context.Background())
		var hostnameError x509.HostnameError
		if err == nil || !errors.As(err, &hostnameError) {
			t.Fatalf("Connect() error = %v, want x509.HostnameError", err)
		}
		<-serverDone
	})
}

func TestClientReconnectsOverTLS(t *testing.T) {
	pki := newClientTestPKI(t)
	listener := newClientTLSListener(t, pki)
	defer listener.Close()

	dropFirst := make(chan struct{})
	secondBound := make(chan struct{})
	serverDone := make(chan error, 2)
	go func() {
		for call := 1; call <= 2; call++ {
			connection, err := listener.Accept()
			if err != nil {
				serverDone <- err
				return
			}
			bind, err := readPacketFrame(connection, 0, 0)
			if err == nil {
				err = writeBindAck(connection, bind, protocol.AckAccepted, "", fmt.Sprintf("session-%d", call))
			}
			if err != nil {
				_ = connection.Close()
				serverDone <- err
				return
			}
			if call == 1 {
				<-dropFirst
				serverDone <- connection.Close()
				continue
			}
			close(secondBound)
			serverDone <- waitForPeerClose(connection)
			_ = connection.Close()
		}
	}()

	client, err := New(Config{
		Address:  listener.Addr().String(),
		ClientID: "client-1",
		DeviceID: "device-1",
		Token:    "token-1",
		TLS:      &TLSConfig{CAFile: pki.caFile},
		Reconnect: &ReconnectConfig{
			InitialDelay: time.Millisecond,
			MaxDelay:     2 * time.Millisecond,
			MaxAttempts:  3,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.config.reconnect.jitter = 0
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	close(dropFirst)

	select {
	case <-secondBound:
	case <-time.After(2 * time.Second):
		t.Fatal("TLS reconnect did not reach the second AUTH/BIND")
	}
	deadline := time.Now().Add(time.Second)
	for (!client.Ready() || client.Binding().SessionID != "session-2") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !client.Ready() || client.Binding().SessionID != "session-2" {
		t.Fatalf("reconnected client ready=%t binding=%+v", client.Ready(), client.Binding())
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for range 2 {
		if err := <-serverDone; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("TLS reconnect server error = %v", err)
		}
	}
}

type clientTestPKI struct {
	caFile     string
	serverCert tls.Certificate
}

func newClientTestPKI(t *testing.T) clientTestPKI {
	t.Helper()
	now := time.Now()
	caPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() CA error = %v", err)
	}
	caPublic := &caPrivate.PublicKey
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Z-Courier Client Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatalf("CreateCertificate() CA error = %v", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ParseCertificate() CA error = %v", err)
	}

	serverPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() server error = %v", err)
	}
	serverPublic := &serverPrivate.PublicKey
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Z-Courier TLS Gateway"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCertificate, serverPublic, caPrivate)
	if err != nil {
		t.Fatalf("CreateCertificate() server error = %v", err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverPrivate)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	serverCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}),
	)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return clientTestPKI{caFile: caFile, serverCert: serverCert}
}

func newClientTLSListener(t *testing.T, pki clientTestPKI) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	return tls.NewListener(listener, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pki.serverCert},
	})
}

func startSingleTLSBindServer(t *testing.T, pki clientTestPKI) (net.Listener, <-chan error) {
	t.Helper()
	listener := newClientTLSListener(t, pki)
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		done <- serveAcceptedBind(connection, false)
	}()
	return listener, done
}
