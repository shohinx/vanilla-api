package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shohinx/vanilla-api/internal/server"
)

const shutdownTimeout = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	application, err := server.NewServer()
	if err != nil {
		return fmt.Errorf("initialize server: %w", err)
	}
	defer func() {
		if err := application.Close(); err != nil {
			slog.Error("close application", slog.Any("error", err))
		}
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- application.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalContext.Done():
		slog.Info("shutting down gracefully; send another signal to force exit")
		stopSignals()
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := application.Shutdown(shutdownContext)
	serveErr := <-serveErrors
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, serveErr)
}
