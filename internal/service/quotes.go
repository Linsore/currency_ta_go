// internal/service/quotes.go
//
// Package service implements the domain logic for the currency quotes service.
// It coordinates validation, background update jobs, caching, and delegation to
// the repository (PostgreSQL) and external exchanger API.
//
// Responsibilities:
//  - Validate currency pairs against an allowlist
//  - Create idempotent update jobs and enqueue them for background processing
//  - Expose read endpoints for update status and last quote
//  - Keep a short-lived in-memory cache for GET /quotes/{pair}
//  - Periodically sweep for "pending" updates left in the database (restart safety)

package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"currency_ta_go/internal/cache"
	"currency_ta_go/internal/external"
	"currency_ta_go/internal/repo"
)

type Service struct {
	allowedPairs map[string]struct{}

	// Dependencies
	repository *repo.Repo
	exchanger  *external.Exchanger
	memCache   *cache.Cache[string, Quote]

	// Background processing
	updatesChan      chan updateJob
	startWorkerOnce  sync.Once
}

type updateJob struct {
	id   uuid.UUID
	pair string
}

type UpdateStatus struct {
	Status    string
	Pair      string
	Price     *float64
	UpdatedAt *time.Time
	Error     string
}

type Quote struct {
	Pair      string
	Price     float64
	UpdatedAt time.Time
}


func New(
	allowedPairs map[string]struct{},
	repository *repo.Repo,
	exchanger *external.Exchanger,
	cache *cache.Cache[string, Quote],
) *Service {
	svc := &Service{
		allowedPairs:     allowedPairs,
		repository:       repository,
		exchanger:        exchanger,
		memCache:         cache,
		updatesChan:      make(chan updateJob, 1024),
	}
	svc.startWorkerOnce.Do(func() { go svc.workerLoop() })
	return svc
}

func ParsePairs(list string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, p := range strings.Split(list, ",") {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p != "" {
			result[p] = struct{}{}
		}
	}
	return result
}

func ToHTTP(err error) int {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrBadRequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

var ErrBadRequest = errors.New("bad request")

func (s *Service) validatePair(pair string) error {
	if len(pair) != 7 || pair[3] != '/' {
		return ErrBadRequest
	}
	if _, ok := s.allowedPairs[pair]; !ok {
		return ErrBadRequest
	}
	return nil
}


func (s *Service) CreateUpdate(ctx context.Context, pair, idempotencyKey string) (uuid.UUID, error) {
	if err := s.validatePair(pair); err != nil {
		return uuid.Nil, err
	}

	// Idempotency: return existing update ID when the same (pair, key) was used.
	if idempotencyKey != "" {
		if len(idempotencyKey) > 128 {
			return uuid.Nil, ErrBadRequest
		}
		if existingID, err := s.repository.FindUpdateByIdem(ctx, pair, idempotencyKey); err == nil {
			return existingID, nil
		}
	}

	updateID := uuid.New()
	var keyPtr *string
	if idempotencyKey != "" {
		keyPtr = &idempotencyKey
	}

	if err := s.repository.InsertUpdate(ctx, updateID, pair, keyPtr); err != nil {
		if strings.Contains(err.Error(), "ux_updates_pair_idem") && idempotencyKey != "" {
			if existingID, e2 := s.repository.FindUpdateByIdem(ctx, pair, idempotencyKey); e2 == nil {
				return existingID, nil
			}
		}
		return uuid.Nil, err
	}

	select {
	case s.updatesChan <- updateJob{id: updateID, pair: pair}:
	default:
	}

	return updateID, nil
}

func (s *Service) GetUpdate(ctx context.Context, updateID uuid.UUID) (UpdateStatus, error) {
	row, err := s.repository.GetUpdate(ctx, updateID)
	if err != nil {
		return UpdateStatus{}, err
	}

	status := UpdateStatus{
		Status: row.Status,
		Pair:   row.Pair,
	}
	if row.Price.Valid {
		status.Price = &row.Price.Float64
	}
	if row.UpdatedAt.Valid {
		status.UpdatedAt = &row.UpdatedAt.Time
	}
	if row.Error.Valid {
		status.Error = row.Error.String
	}
	return status, nil
}

func (s *Service) GetLastQuote(ctx context.Context, pair string) (Quote, error) {
	if err := s.validatePair(pair); err != nil {
		return Quote{}, ErrBadRequest
	}

	if cached, ok := s.memCache.Get(pair); ok {
		return cached, nil
	}

	row, err := s.repository.GetQuote(ctx, pair)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Quote{}, repo.ErrNotFound
		}
		return Quote{}, err
	}

	quote := Quote{
		Pair:      row.Pair,
		Price:     row.Price,
		UpdatedAt: row.UpdatedAt,
	}
	s.memCache.Set(pair, quote)
	return quote, nil
}


func (s *Service) workerLoop() {
	sweepTicker := time.NewTicker(30 * time.Second)
	defer sweepTicker.Stop()

	for {
		select {
		case job := <-s.updatesChan:
			_ = s.processUpdateJob(job)
		case <-sweepTicker.C:
			ctx := context.Background()
			rows, err := s.repository.EnqueuePending(ctx, 100)
			if err == nil {
				for _, row := range rows {
					select {
					case s.updatesChan <- updateJob{id: row.ID, pair: row.Pair}:
					default:
					}
				}
			}
		}
	}
}

func (s *Service) processUpdateJob(job updateJob) error {
	ctx := context.Background()

	base, quote, _ := strings.Cut(job.pair, "/")

	price, err := s.exchanger.Convert(ctx, base, quote)
	now := time.Now().UTC()

	if err != nil {
		_ = s.repository.SetUpdateError(ctx, job.id, err.Error(), now)
		return err
	}

	rounded := roundToDecimals(price, 6)

	if err := s.repository.SetUpdateDoneAndUpsertQuote(ctx, job.id, job.pair, rounded, now); err != nil {
		return err
	}

	s.memCache.Delete(job.pair)
	return nil
}

func roundToDecimals(x float64, n int) float64 {
	p := math.Pow(10, float64(n))
	return math.Round(x*p) / p
}
