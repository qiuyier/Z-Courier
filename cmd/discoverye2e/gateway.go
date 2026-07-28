package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type gatewayProcess struct {
	command *exec.Cmd
	logFile *os.File
	logPath string
	done    chan struct{}

	mu      sync.Mutex
	waitErr error
}

func startGatewayProcess(configuration config) (*gatewayProcess, error) {
	logDirectory := filepath.Dir(configuration.GatewayLog)
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create gateway log directory: %w", err)
	}
	logFile, err := os.OpenFile(configuration.GatewayLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open gateway log: %w", err)
	}

	command := exec.Command(configuration.GatewayBin, "-config", configuration.GatewayConfig)
	command.Env = replaceEnvironment(
		os.Environ(),
		"ZINX_CONFIG_FILE_PATH",
		configuration.ZinxConfig,
	)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start gateway: %w", err)
	}

	process := &gatewayProcess{
		command: command,
		logFile: logFile,
		logPath: configuration.GatewayLog,
		done:    make(chan struct{}),
	}
	go func() {
		waitErr := command.Wait()
		process.mu.Lock()
		process.waitErr = waitErr
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (process *gatewayProcess) Stop() error {
	if process == nil {
		return nil
	}

	select {
	case <-process.done:
	default:
		if err := process.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("signal gateway shutdown: %w", err)
		}
		select {
		case <-process.done:
		case <-time.After(12 * time.Second):
			if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("kill gateway after shutdown timeout: %w", err)
			}
			<-process.done
		}
	}

	process.mu.Lock()
	waitErr := process.waitErr
	process.mu.Unlock()
	closeErr := process.logFile.Close()
	if waitErr != nil {
		return errors.Join(fmt.Errorf("gateway exited: %w", waitErr), closeErr)
	}
	return closeErr
}

func (process *gatewayProcess) ExitError() error {
	if process == nil {
		return errors.New("gateway process is nil")
	}
	select {
	case <-process.done:
		process.mu.Lock()
		defer process.mu.Unlock()
		if process.waitErr != nil {
			return fmt.Errorf("gateway exited unexpectedly: %w", process.waitErr)
		}
		return errors.New("gateway exited unexpectedly")
	default:
		return nil
	}
}

func waitForGatewayReady(ctx context.Context, process *gatewayProcess, readyURL string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := process.ExitError(); err != nil {
			return err
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return fmt.Errorf("create readiness request: %w", err)
		}
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for gateway readiness: %w", ctx.Err())
		case <-process.done:
			return process.ExitError()
		case <-ticker.C:
		}
	}
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}

func printGatewayLogTail(path string, maximumBytes int64) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return
	}
	offset := info.Size() - maximumBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n--- gateway log tail (%s) ---\n%s\n", path, data)
}
