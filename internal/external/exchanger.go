// internal/external/exchanger.go
package external


import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)


type Exchanger struct {
	baseURL    string
	httpClient *http.Client
	Debug      bool // set true to log request and payload
}
func New(base string, timeout time.Duration) *Exchanger {
	return &Exchanger{
		baseURL:    strings.TrimRight(base, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}
func (e *Exchanger) Convert(ctx context.Context, from, to string) (float64, error) {
	u, _ := url.Parse(e.baseURL + "/latest")

	q := u.Query()
	// Frankfurter accepts either "base" or "from"; their docs show "from" but "base" is canonical.
	q.Set("base", strings.ToUpper(from))
	q.Set("symbols", strings.ToUpper(to))
	u.RawQuery = q.Encode()
	if e.Debug {
		log.Printf("fx call: GET %s", u.String())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	
	const maxDump = 8 * 1024 // 8KB cap for logs

	if e.Debug {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxDump))
		log.Printf("fx resp: %d body=%s", resp.StatusCode, string(bytes.TrimSpace(raw)))
		// put bytes back so the JSON decoder can read them
		resp.Body = io.NopCloser(bytes.NewReader(raw))
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("external status %d", resp.StatusCode)
	}

	var payload struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	rate, ok := payload.Rates[strings.ToUpper(to)]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("no rate for %s/%s", from, to)
	}
	return rate, nil
}