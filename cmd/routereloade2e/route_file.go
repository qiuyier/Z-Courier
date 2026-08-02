package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type routeFileDocument struct {
	Version int              `yaml:"version"`
	Routes  []routeFileRoute `yaml:"routes"`
}

type routeFileRoute struct {
	Name     string          `yaml:"name"`
	Enabled  bool            `yaml:"enabled"`
	MsgIDMin uint32          `yaml:"msg_id_min"`
	MsgIDMax uint32          `yaml:"msg_id_max"`
	Target   routeFileTarget `yaml:"target"`
}

type routeFileTarget struct {
	Type        string `yaml:"type"`
	URL         string `yaml:"url"`
	Token       string `yaml:"token"`
	Timeout     string `yaml:"timeout"`
	MaxInFlight int    `yaml:"max_in_flight"`
}

func routeDocumentForBackendA(address string) routeFileDocument {
	return routeFileDocument{
		Version: 1,
		Routes: []routeFileRoute{
			newHTTPRoute("route-reload-a", primaryMsgID, address),
		},
	}
}

func routeDocumentForBackendB(address string) routeFileDocument {
	return routeFileDocument{
		Version: 1,
		Routes: []routeFileRoute{
			newHTTPRoute("route-reload-b-primary", primaryMsgID, address),
			newHTTPRoute("route-reload-b-added", addedMsgID, address),
		},
	}
}

func routeDocumentOutsideEnvelope(address string) routeFileDocument {
	return routeFileDocument{
		Version: 1,
		Routes: []routeFileRoute{
			newHTTPRoute("route-reload-outside-envelope", outOfEnvelopeMsgID, address),
		},
	}
}

func newHTTPRoute(name string, msgID uint32, address string) routeFileRoute {
	return routeFileRoute{
		Name:     name,
		Enabled:  true,
		MsgIDMin: msgID,
		MsgIDMax: msgID,
		Target: routeFileTarget{
			Type:        "http",
			URL:         "http://" + address + "/gateway/upstream",
			Token:       defaultUpstreamToken,
			Timeout:     "5s",
			MaxInFlight: 16,
		},
	}
}

func writeRouteDocumentAtomic(path string, document routeFileDocument) error {
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal route document: %w", err)
	}
	return writeRouteBytesAtomic(path, data)
}

func writeRouteBytesAtomic(path string, data []byte) (writeErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create route file directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".upstream-routes-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary route file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			writeErr = errors.Join(writeErr, temporary.Close())
		}
		if writeErr != nil {
			writeErr = errors.Join(writeErr, os.Remove(temporaryPath))
		}
	}()

	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set temporary route file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary route file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary route file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary route file: %w", err)
	}
	temporary = nil
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace route file: %w", err)
	}
	return nil
}
