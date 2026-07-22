package webhookpublisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/tlsconfig"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const testSecret = "0123456789abcdef0123456789abcdef"
const previousTestSecret = "abcdef0123456789abcdef0123456789"

func TestPublisherPostsSignedBody(t *testing.T) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{"gateway-terminal": []byte(testSecret)},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	body := []byte(`{"event_id":"message-1:failed"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != contentTypeJSON {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(got) != string(body) {
			t.Fatalf("body = %q, want %q", got, body)
		}
		if err := verifier.Verify(r, got); err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	publisher := newTestPublisher(t, server.URL)
	if err := publisher.Publish(context.Background(), body); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestPublisherRotationOverlapAcceptsOldAndNewKeys(t *testing.T) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{
			"gateway-terminal-2026-01": []byte(previousTestSecret),
			"gateway-terminal-2026-02": []byte(testSecret),
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	accepted := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("ReadAll() error = %v", readErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if verifyErr := verifier.Verify(r, body); verifyErr != nil {
			t.Errorf("Verify() error = %v", verifyErr)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		accepted <- r.Header.Get(signing.HeaderKeyID)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	for _, key := range []struct {
		id     string
		secret string
	}{
		{id: "gateway-terminal-2026-01", secret: previousTestSecret},
		{id: "gateway-terminal-2026-02", secret: testSecret},
	} {
		signer, signerErr := signing.NewSigner(signing.SignerConfig{KeyID: key.id, Secret: []byte(key.secret)})
		if signerErr != nil {
			t.Fatalf("NewSigner(%q) error = %v", key.id, signerErr)
		}
		publisher, publisherErr := New(Config{URL: server.URL, Signer: signer})
		if publisherErr != nil {
			t.Fatalf("New(%q) error = %v", key.id, publisherErr)
		}
		if publisherErr = publisher.Publish(context.Background(), []byte(`{"event_id":"rotation-probe"}`)); publisherErr != nil {
			publisher.Close()
			t.Fatalf("Publish(%q) error = %v", key.id, publisherErr)
		}
		publisher.Close()
	}

	seen := map[string]bool{}
	for range 2 {
		seen[<-accepted] = true
	}
	if !seen["gateway-terminal-2026-01"] || !seen["gateway-terminal-2026-02"] {
		t.Fatalf("accepted key IDs = %v, want old and new", seen)
	}
}

func TestPublisherRejectsNon2xxAndRedirects(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		err := newTestPublisher(t, server.URL).Publish(context.Background(), []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("Publish() error = %v, want status 503", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		redirected := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/target" {
				redirected = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
		}))
		defer server.Close()

		err := newTestPublisher(t, server.URL).Publish(context.Background(), []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "307") {
			t.Fatalf("Publish() error = %v, want status 307", err)
		}
		if redirected {
			t.Fatal("redirect target was called")
		}
	})
}

func TestPublisherUsesPrivateCA(t *testing.T) {
	pki := newTestPKI(t)
	server := newTestTLSServer(t, pki, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	trusted, err := New(Config{
		URL:    server.URL,
		Signer: newTestSigner(t),
		TLS:    tlsconfig.Files{CAFile: pki.caFile},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer trusted.Close()
	if err := trusted.Publish(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("Publish() with private CA error = %v", err)
	}

	untrusted := newTestPublisher(t, server.URL)
	defer untrusted.Close()
	if err := untrusted.Publish(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("Publish() without private CA error = nil")
	}

	wrongName, err := New(Config{
		URL:    server.URL,
		Signer: newTestSigner(t),
		TLS: tlsconfig.Files{
			CAFile:     pki.caFile,
			ServerName: "wrong.receiver.local",
		},
	})
	if err != nil {
		t.Fatalf("New() wrong server name error = %v", err)
	}
	defer wrongName.Close()
	if err := wrongName.Publish(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("Publish() with wrong server name error = nil")
	}
}

func TestPublisherUsesMutualTLS(t *testing.T) {
	pki := newTestPKI(t)
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{"gateway-terminal": []byte(testSecret)},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	clientCommonName := make(chan string, 1)
	server := newTestTLSServer(t, pki, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || verifier.Verify(r, body) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			clientCommonName <- r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	withoutClient, err := New(Config{
		URL:    server.URL,
		Signer: newTestSigner(t),
		TLS:    tlsconfig.Files{CAFile: pki.caFile},
	})
	if err != nil {
		t.Fatalf("New() without client certificate error = %v", err)
	}
	defer withoutClient.Close()
	if err := withoutClient.Publish(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("Publish() without client certificate error = nil")
	}

	withClient, err := New(Config{
		URL:    server.URL,
		Signer: newTestSigner(t),
		TLS: tlsconfig.Files{
			CAFile:         pki.caFile,
			ClientCertFile: pki.clientCertFile,
			ClientKeyFile:  pki.clientKeyFile,
		},
	})
	if err != nil {
		t.Fatalf("New() with client certificate error = %v", err)
	}
	defer withClient.Close()
	if err := withClient.Publish(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("Publish() with mTLS error = %v", err)
	}
	select {
	case got := <-clientCommonName:
		if got != "z-courier-gateway" {
			t.Fatalf("client certificate common name = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not observe client certificate")
	}
}

func TestNewSetsMinimumTLSVersion(t *testing.T) {
	publisher, err := New(Config{URL: "https://receiver.local", Signer: newTestSigner(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer publisher.Close()
	transport, ok := publisher.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", publisher.client.Transport)
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS config = %+v, want minimum TLS 1.2", transport.TLSClientConfig)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	signer := newTestSigner(t)
	firstPKI := newTestPKI(t)
	secondPKI := newTestPKI(t)
	invalidCAFile := filepath.Join(t.TempDir(), "invalid-ca.crt")
	if err := os.WriteFile(invalidCAFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		config Config
		want   string
	}{
		{name: "empty URL", config: Config{Signer: signer}, want: "absolute http or https URL"},
		{name: "invalid scheme", config: Config{URL: "ftp://receiver.local", Signer: signer}, want: "absolute http or https URL"},
		{name: "missing signer", config: Config{URL: "https://receiver.local"}, want: "signer is required"},
		{name: "TLS requires HTTPS", config: Config{URL: "http://receiver.local", Signer: signer, TLS: tlsconfig.Files{ServerName: "receiver.local"}}, want: "TLS settings require an https URL"},
		{name: "client certificate requires key", config: Config{URL: "https://receiver.local", Signer: signer, TLS: tlsconfig.Files{ClientCertFile: "tls.crt"}}, want: "configured together"},
		{name: "client certificate and key mismatch", config: Config{URL: "https://receiver.local", Signer: signer, TLS: tlsconfig.Files{ClientCertFile: firstPKI.clientCertFile, ClientKeyFile: secondPKI.clientKeyFile}}, want: "private key does not match public key"},
		{name: "invalid CA PEM", config: Config{URL: "https://receiver.local", Signer: signer, TLS: tlsconfig.Files{CAFile: invalidCAFile}}, want: "no valid PEM certificates"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
		})
	}
}

type testPKI struct {
	caFile         string
	clientCertFile string
	clientKeyFile  string
	serverCert     tls.Certificate
	clientRoots    *x509.CertPool
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	now := time.Now()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() CA error = %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Z-Courier Test CA"},
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
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	serverCertPEM, serverKeyPEM := issueTestCertificate(
		t,
		caCertificate,
		caPrivate,
		big.NewInt(2),
		"terminal-webhook",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		[]net.IP{net.ParseIP("127.0.0.1")},
	)
	serverCertificate, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() server error = %v", err)
	}
	clientCertPEM, clientKeyPEM := issueTestCertificate(
		t,
		caCertificate,
		caPrivate,
		big.NewInt(3),
		"z-courier-gateway",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		nil,
	)

	directory := t.TempDir()
	caFile := filepath.Join(directory, "ca.crt")
	clientCertFile := filepath.Join(directory, "client.crt")
	clientKeyFile := filepath.Join(directory, "client.key")
	for path, contents := range map[string][]byte{
		caFile:         caPEM,
		clientCertFile: clientCertPEM,
		clientKeyFile:  clientKeyPEM,
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(caCertificate)
	return testPKI{
		caFile:         caFile,
		clientCertFile: clientCertFile,
		clientKeyFile:  clientKeyFile,
		serverCert:     serverCertificate,
		clientRoots:    clientRoots,
	}
}

func issueTestCertificate(
	t *testing.T,
	caCertificate *x509.Certificate,
	caPrivate ed25519.PrivateKey,
	serialNumber *big.Int,
	commonName string,
	extendedKeyUsage []x509.ExtKeyUsage,
	ipAddresses []net.IP,
) ([]byte, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() %s error = %v", commonName, err)
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  extendedKeyUsage,
		IPAddresses:  ipAddresses,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, caCertificate, publicKey, caPrivate)
	if err != nil {
		t.Fatalf("CreateCertificate() %s error = %v", commonName, err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() %s error = %v", commonName, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
}

func newTestTLSServer(t *testing.T, pki testPKI, requireClient bool, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pki.serverCert},
	}
	if requireClient {
		server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		server.TLS.ClientCAs = pki.clientRoots
	}
	server.StartTLS()
	return server
}

func newTestPublisher(t *testing.T, rawURL string) *Publisher {
	t.Helper()
	publisher, err := New(Config{URL: rawURL, Signer: newTestSigner(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return publisher
}

func newTestSigner(t *testing.T) *signing.Signer {
	t.Helper()
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "gateway-terminal", Secret: []byte(testSecret)})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	return signer
}
