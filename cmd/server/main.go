package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"provision/api"
	"provision/database"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Configuration
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "provision.db"
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	// Initialize Logger using log/slog
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("Starting Provision server",
		slog.String("port", port),
		slog.String("db_path", dbPath),
		slog.String("data_dir", dataDir),
	)

	// Initialize Storage
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Error("Failed to create data directory", slog.String("error", err.Error()), slog.String("path", dataDir))
		os.Exit(1)
	}

	// Initialize Database
	db, err := database.OpenDatabase(dbPath)
	if err != nil {
		logger.Error("Failed to initialize database", slog.String("error", err.Error()), slog.String("path", dbPath))
		os.Exit(1)
	}
	defer db.Close()

	// Initialize API State
	apiState := &api.Server{
		DB:      db,
		DataDir: dataDir,
		Logger:  logger,
	}

	// Setup Router
	mux := http.NewServeMux()

	noAuthRequired := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, apiState.LoggingMiddleware(handler))
	}

	withAuth := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, apiState.LoggingMiddleware(apiState.AuthMiddleware(handler)))
	}

	// Product Endpoints
	noAuthRequired("GET /api/v1/products", apiState.HandleProducts)
	noAuthRequired("POST /api/v1/products", apiState.HandleProducts)
	withAuth("DELETE /api/v1/products/{id}", apiState.HandleProductDelete)

	// Version Endpoints
	withAuth("POST /api/v1/products/{product_id}/versions/initial", apiState.HandleInitialVersion)
	withAuth("POST /api/v1/products/{product_id}/versions/update", apiState.HandleUpdateVersion)

	// Client Endpoints
	noAuthRequired("GET /api/v1/products/{product_id}/download", apiState.HandleDownloadLatest)
	noAuthRequired("GET /api/v1/products/{product_id}/check", apiState.HandleCheckUpdate)
	noAuthRequired("GET /api/v1/products/{product_id}/patch", apiState.HandlePatch)

	// Health Check
	mux.HandleFunc("GET /", apiState.LoggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		apiState.SendJSONResponse(w, http.StatusOK, map[string]string{"message": "Welcome to Provision"})
	}))

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second, // Longer for large downloads
		IdleTimeout:  120 * time.Second,
	}

	// Server run context
	serverCtx, serverStopCtx := context.WithCancel(context.Background())

	// Listen for syscall signals for process to interrupt/quit
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-sig

		// Shutdown signal with grace period of 30 seconds
		shutdownCtx, cancel := context.WithTimeout(serverCtx, 30*time.Second)
		defer cancel()

		go func() {
			<-shutdownCtx.Done()
			if shutdownCtx.Err() == context.DeadlineExceeded {
				logger.Error("graceful shutdown timed out.. forcing exit.")
				os.Exit(1)
			}
		}()

		// Trigger graceful shutdown
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			logger.Error("Server shutdown failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		serverStopCtx()
	}()

	logger.Info("Server listening", slog.String("port", port))
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Wait for server context to be stopped
	<-serverCtx.Done()
	logger.Info("HTTP server stopped gracefully, waiting for background jobs...")
	apiState.Wait()
	logger.Info("Server stopped gracefully")
}
