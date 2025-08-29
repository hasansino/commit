package commit

import (
	"log/slog"
	"testing"
	"time"
)

func TestWithLogger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	settings := &Settings{
		Timeout: 30 * time.Second,
	}

	service := &Service{
		settings: settings,
	}

	option := WithLogger(logger)
	option(service)

	if service.logger != logger {
		t.Errorf("WithLogger() failed to set logger, got %v, want %v", service.logger, logger)
	}
}

func TestWithLogger_Nil(t *testing.T) {
	settings := &Settings{
		Timeout: 30 * time.Second,
	}

	service := &Service{
		settings: settings,
	}

	option := WithLogger(nil)
	option(service)

	if service.logger != nil {
		t.Errorf("WithLogger(nil) should set logger to nil, got %v", service.logger)
	}
}
