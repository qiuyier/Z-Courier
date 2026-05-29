package server

import "go.uber.org/zap"

func defaultLogger() *zap.Logger {
	logger, err := zap.NewProduction()
	if err != nil {
		return zap.NewNop()
	}

	return logger
}
