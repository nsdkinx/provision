package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"provision/product"
	"sync"
	"time"
)

// Server holds the application dependencies and state.
type Server struct {
	DB      *sql.DB
	DataDir string
	Logger  *slog.Logger
	wg      sync.WaitGroup // Tracks background tasks like zip compression
}

// Config controls server-wide options.
type Config struct {
	MaxUploadSize int64
}

// APIError represents a structured error response.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// responseWriter is a wrapper for http.ResponseWriter that captures the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

// LoggingMiddleware logs details of each HTTP request.
func (server *Server) LoggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{w, 0}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}

		server.Logger.Info("HTTP Request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", duration),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
		)
	}
}

// AuthMiddleware validates the X-API-Key header.
func (server *Server) AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			server.SendError(w, http.StatusForbidden, "MISSING_API_KEY", "Missing API Key")
			return
		}

		productID := r.PathValue("product_id")
		if productID == "" {
			productID = r.PathValue("id")
		}

		if productID != "" {
			if !product.ValidateProductKey(r.Context(), server.DB, productID, apiKey) {
				server.SendError(w, http.StatusForbidden, "INVALID_API_KEY", "Invalid API Key")
				return
			}
		} else {
			// Global endpoint like GET /products (usually requires admin key)
			adminKey := os.Getenv("ADMIN_KEY")
			if adminKey == "" || apiKey != adminKey {
				server.SendError(w, http.StatusForbidden, "INVALID_ADMIN_KEY", "Invalid or missing Admin Key")
				return
			}
		}

		next.ServeHTTP(w, r)
	}
}

// SendJSONResponse sends a JSON response with the given status code.
func (server *Server) SendJSONResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		server.Logger.Error("Failed to encode JSON response", slog.String("error", err.Error()))
	}
}

// SendError sends a structured JSON error response.
func (server *Server) SendError(w http.ResponseWriter, status int, code, message string) {
	server.Logger.Warn("API Error",
		slog.Int("status", status),
		slog.String("code", code),
		slog.String("message", message),
	)
	server.SendJSONResponse(w, status, APIError{
		Code:    code,
		Message: message,
	})
}

// RunAsync executes a task in a separate goroutine and tracks it.
func (server *Server) RunAsync(task func()) {
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		task()
	}()
}

// Wait blocks until all background tasks are complete.
func (server *Server) Wait() {
	server.wg.Wait()
}
