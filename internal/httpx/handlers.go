// internal/httpx/handlers.go
//
// Package httpx exposes HTTP handlers for the currency quotes service.
// It wires the service layer to HTTP endpoints and performs request
// parsing/validation plus JSON serialization. Business logic remains in service.

package httpx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"log"

	"github.com/google/uuid"
	"currency_ta_go/internal/service"
)

// Route path prefixes used for tail extraction.
const (
	updatePrefix = "/quotes/update/"
	quotePrefix  = "/quotes/"
)

// Server bundles the HTTP handlers with the underlying service.
type Server struct {
	svc *service.Service
}

// Middleware defines a function that wraps an http.Handler (e.g., rate limiting).
type Middleware func(http.Handler) http.Handler

// NewMux creates an HTTP multiplexer with all routes registered and wrapped
// by the provided middleware (such as a rate limiter).
func NewMux(svc *service.Service, middleware Middleware) http.Handler {
	server := &Server{svc: svc}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/quotes/update", middleware(http.HandlerFunc(server.handleCreateUpdate))) // POST
	mux.Handle("/quotes/update/", middleware(http.HandlerFunc(server.handleGetByUpdateID))) // GET /quotes/update/{uuid}
	mux.Handle("/quotes/", middleware(http.HandlerFunc(server.handleGetLastQuote)))         // GET /quotes/{pair}

	// open: http://localhost:8080/swagger/index.html
	return mux
}

// ---- DTOs (request/response shapes) ----

// createUpdateRequest describes the POST body for starting an update job.
type createUpdateRequest struct {
	Pair string `json:"pair"`
}

// createUpdateResponse is returned after enqueuing an update job.
type createUpdateResponse struct {
	UpdateID string `json:"update_id"`
}

// updateStatusResponse returns the status/result of a specific update job.
type updateStatusResponse struct {
	Status    string     `json:"status"`                 // pending | done | error
	Pair      string     `json:"pair"`
	Price     *float64   `json:"price,omitempty"`        // present when status == done
	UpdatedAt *time.Time `json:"updated_at,omitempty"`   // present when status == done
	Error     string     `json:"error,omitempty"`        // present when status == error
}

// lastQuoteResponse returns the latest stored quote for a pair.
type lastQuoteResponse struct {
	Pair      string    `json:"pair"`
	Price     float64   `json:"price"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---- Handlers ----

// handleCreateUpdate starts an asynchronous update for a currency pair.
// Method: POST /quotes/update
// Body: { "pair": "EUR/MXN" }
// Optional header: Idempotency-Key (string <= 128 chars)
// Response: 200 { "update_id": "<uuid>" }
// @Summary      Request async quote update
// @Tags         quotes
// @Accept       json
// @Produce      json
// @Param        Idempotency-Key header string false "Idempotency key"
// @Param        payload body   createUpdateRequest true "Update request"
// @Success      200 {object}   createUpdateResponse
// @Failure      400 {object}   map[string]string
// @Router       /quotes/update [post]
func (s *Server) handleCreateUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var req createUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	pair := normalizePair(req.Pair)

	updateID, err := s.svc.CreateUpdate(r.Context(), pair, idempotencyKey)
	if err != nil {
		writeError(w, service.ToHTTP(err), err.Error())
		return
	}

	writeJSON(w, http.StatusOK, createUpdateResponse{UpdateID: updateID.String()})
}

// handleGetByUpdateID returns the status/result for a specific update job.
// Method: GET /quotes/update/{id}
// Response: 200 { status, pair, ... } | 404
func (s *Server) handleGetByUpdateID(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, updatePrefix)
	updateID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	status, err := s.svc.GetUpdate(r.Context(), updateID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := updateStatusResponse{
		Status:    status.Status,
		Pair:      status.Pair,
		Price:     status.Price,
		UpdatedAt: status.UpdatedAt,
		Error:     status.Error,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetLastQuote returns the last known quote for the given pair.
// Method: GET /quotes/{pair}
// Response: 200 { pair, price, updated_at } | 4xx/5xx via service.ToHTTP

func (s *Server) handleGetLastQuote(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	pair := normalizePair(strings.TrimPrefix(r.URL.Path, quotePrefix))

	quote, err := s.svc.GetLastQuote(r.Context(), pair)
	if err != nil {
		writeError(w, service.ToHTTP(err), err.Error())
		return
	}

	writeJSON(w, http.StatusOK, lastQuoteResponse{
		Pair:      quote.Pair,
		Price:     quote.Price,
		UpdatedAt: quote.UpdatedAt,
	})
}

// ---- Helpers ----

// requireMethod checks that r.Method matches expected and writes a 405 if it does not.
func requireMethod(w http.ResponseWriter, r *http.Request, expected string) bool {
	if r.Method == expected {
		return true
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

// normalizePair uppercases and trims the incoming pair string.
func normalizePair(pair string) string {
	return strings.ToUpper(strings.TrimSpace(pair))
}

// writeJSON serializes v to JSON with the provided status code and sets the Content-Type.
func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a simple JSON error response with the given status code.
func writeError(w http.ResponseWriter, statusCode int, message string) {
	type errorEnvelope struct {
		Error string `json:"error"`
	}
	log.Printf("http error: status=%d message=%q", statusCode, message)
	writeJSON(w, statusCode, errorEnvelope{Error: message})
}
