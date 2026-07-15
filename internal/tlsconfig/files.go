// Package tlsconfig builds bounded TLS client configurations from certificate files.
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
)

type Files struct {
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	ServerName     string
}

func (f Files) Configured() bool {
	return strings.TrimSpace(f.CAFile) != "" ||
		strings.TrimSpace(f.ClientCertFile) != "" ||
		strings.TrimSpace(f.ClientKeyFile) != "" ||
		strings.TrimSpace(f.ServerName) != ""
}

func Build(files Files) (*tls.Config, error) {
	files = normalized(files)
	if (files.ClientCertFile == "") != (files.ClientKeyFile == "") {
		return nil, fmt.Errorf("client_cert_file and client_key_file must be configured together")
	}
	if err := validateServerName(files.ServerName); err != nil {
		return nil, err
	}

	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: files.ServerName,
	}
	if files.CAFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		contents, err := os.ReadFile(files.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file %q: %w", files.CAFile, err)
		}
		if !roots.AppendCertsFromPEM(contents) {
			return nil, fmt.Errorf("parse ca_file %q: no valid PEM certificates", files.CAFile)
		}
		config.RootCAs = roots
	}
	if files.ClientCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(files.ClientCertFile, files.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf(
				"load client certificate %q and key %q: %w",
				files.ClientCertFile,
				files.ClientKeyFile,
				err,
			)
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return config, nil
}

func Validate(files Files) error {
	_, err := Build(files)
	return err
}

func normalized(files Files) Files {
	files.CAFile = strings.TrimSpace(files.CAFile)
	files.ClientCertFile = strings.TrimSpace(files.ClientCertFile)
	files.ClientKeyFile = strings.TrimSpace(files.ClientKeyFile)
	files.ServerName = strings.TrimSpace(files.ServerName)
	return files
}

func validateServerName(value string) error {
	if value == "" {
		return nil
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/\\[] \t\r\n") {
		return fmt.Errorf("server_name must be a DNS name or IP address without a scheme, port, or path")
	}
	if net.ParseIP(value) == nil {
		if _, _, err := net.SplitHostPort(value); err == nil || strings.Contains(value, ":") {
			return fmt.Errorf("server_name must not include a port")
		}
	}
	return nil
}
