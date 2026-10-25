package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrNotFound — источник не нашёл субъект по ИНН.
var ErrNotFound = errors.New("not found")

// Client — HTTP-клиент к внешним источникам с таймаутом и повторными попытками.
type Client struct {
	baseURL string
	http    *http.Client
	retries int
	backoff time.Duration
}

func NewClient(baseURL string, timeout time.Duration, retries int) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
		retries: retries,
		backoff: 300 * time.Millisecond,
	}
}

// CallResult — метаданные вызова (для журнала обогащения).
type CallResult struct {
	Attempts   int
	DurationMS int
}

func (c *Client) get(ctx context.Context, path string, out any) (CallResult, error) {
	start := time.Now()
	var lastErr error
	res := CallResult{}
	for attempt := 1; attempt <= c.retries+1; attempt++ {
		res.Attempts = attempt
		lastErr = c.doGet(ctx, path, out)
		if lastErr == nil || errors.Is(lastErr, ErrNotFound) || ctx.Err() != nil {
			break
		}
		if attempt <= c.retries {
			select {
			case <-time.After(c.backoff * time.Duration(attempt)):
			case <-ctx.Done():
			}
		}
	}
	res.DurationMS = int(time.Since(start).Milliseconds())
	return res, lastErr
}

func (c *Client) doGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("запрос не выполнен: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("источник ответил %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("некорректный ответ источника: %w", err)
	}
	return nil
}

func (c *Client) EGRUL(ctx context.Context, inn string) (EGRULCompany, CallResult, error) {
	var out EGRULCompany
	r, err := c.get(ctx, "/egrul/v1/companies/"+url.PathEscape(inn), &out)
	return out, r, err
}

func (c *Client) BKI(ctx context.Context, inn string) (BKIReport, CallResult, error) {
	var out BKIReport
	r, err := c.get(ctx, "/bki/v1/reports/"+url.PathEscape(inn), &out)
	return out, r, err
}

func (c *Client) FNS(ctx context.Context, inn string) (FNSInfo, CallResult, error) {
	var out FNSInfo
	r, err := c.get(ctx, "/fns/v1/transparent-business/"+url.PathEscape(inn), &out)
	return out, r, err
}
