//go:build e2e

// Сквозные тесты против запущенного стенда (docker compose up).
// Запуск: E2E_BASE_URL=http://localhost:8080 go test -tags e2e -v ./e2e
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"
)

var base = func() string {
	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}()

type client struct {
	t     *testing.T
	token string
}

func login(t *testing.T, email string) *client {
	t.Helper()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	c := &client{t: t}
	c.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "demo1234"}, 200, &out)
	c.token = out.AccessToken
	return c
}

func (c *client) do(method, path string, body any, want int, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	c.send(req, want, out)
}

func (c *client) send(req *http.Request, want int, out any) {
	c.t.Helper()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", req.Method, req.URL.Path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("decode: %v: %s", err, data)
		}
	}
}

type view struct {
	Application struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
		Offer   *struct {
			Amount     int64 `json:"amount"`
			TermMonths int   `json:"term_months"`
		} `json:"offer"`
	} `json:"application"`
	SignCodeDemo     string `json:"sign_code_demo"`
	DocumentRequests []struct {
		ID string `json:"id"`
	} `json:"document_requests"`
}

// apply создаёт черновик, отправляет и ждёт выхода из автоматической обработки.
func apply(c *client, product, purpose string, amount int64, term int, revenue int64) view {
	c.t.Helper()
	var v view
	c.do("POST", "/api/v1/applications", map[string]any{"product_code": product, "purpose": purpose,
		"amount": amount, "term_months": term}, 201, &v)
	id := v.Application.ID
	c.do("PATCH", "/api/v1/applications/"+id, map[string]any{"version": v.Application.Version, "product_code": product,
		"purpose": purpose, "amount": amount, "term_months": term, "annual_revenue": revenue,
		"contact_phone": "+7 999 123-45-67", "bki_consent": true}, 200, &v)
	c.do("POST", "/api/v1/applications/"+id+"/submit", nil, 200, &v)
	return waitFor(c, id, "MANUAL_REVIEW", "APPROVED", "COUNTER_OFFER", "REJECTED")
}

func waitFor(c *client, id string, statuses ...string) view {
	c.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var v view
		c.do("GET", "/api/v1/applications/"+id, nil, 200, &v)
		for _, s := range statuses {
			if v.Application.Status == s {
				return v
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	c.t.Fatalf("application %s did not reach %v", id, statuses)
	return view{}
}

func TestAutoApprovalToIssue(t *testing.T) {
	c := login(t, "anna@romashka.demo")
	v := apply(c, "EXPRESS", "CASH_GAP", 3_000_000, 24, 60_000_000)
	if v.Application.Status != "APPROVED" {
		t.Fatalf("status %s, want APPROVED", v.Application.Status)
	}
	id := v.Application.ID
	c.do("POST", "/api/v1/applications/"+id+"/offer/accept", nil, 200, &v)
	if v.SignCodeDemo == "" {
		t.Fatal("sign code expected")
	}
	c.do("POST", "/api/v1/applications/"+id+"/sign", map[string]string{"code": "000000x"}, 422, nil)
	c.do("POST", "/api/v1/applications/"+id+"/sign", map[string]string{"code": v.SignCodeDemo}, 200, &v)
	waitFor(c, id, "ISSUED")
}

func TestCounterOffer(t *testing.T) {
	c := login(t, "dmitry@vector.demo")
	v := apply(c, "WORKING_CAPITAL", "WORKING_CAPITAL", 10_000_000, 24, 12_000_000)
	if v.Application.Status != "COUNTER_OFFER" || v.Application.Offer == nil {
		t.Fatalf("status %s, want COUNTER_OFFER with offer", v.Application.Status)
	}
	c.do("POST", "/api/v1/applications/"+v.Application.ID+"/offer/decline", nil, 200, &v)
	if v.Application.Status != "DECLINED_BY_CLIENT" {
		t.Fatalf("status %s", v.Application.Status)
	}
}

func TestStopFactorReject(t *testing.T) {
	c := login(t, "sergey@gorizont.demo")
	v := apply(c, "WORKING_CAPITAL", "WORKING_CAPITAL", 5_000_000, 24, 31_000_000)
	if v.Application.Status != "REJECTED" {
		t.Fatalf("status %s, want REJECTED", v.Application.Status)
	}
}

func TestManualReviewWithDocuments(t *testing.T) {
	cl := login(t, "igor@severov.demo")
	v := apply(cl, "EXPRESS", "CASH_GAP", 1_500_000, 24, 9_000_000)
	if v.Application.Status != "MANUAL_REVIEW" {
		t.Fatalf("status %s, want MANUAL_REVIEW", v.Application.Status)
	}
	id := v.Application.ID
	uw := login(t, "olga@bank.demo")
	// клиент не может принимать решения, андеррайтер — действовать за клиента
	cl.do("POST", "/api/v1/applications/"+id+"/decision", map[string]any{"action": "APPROVE"}, 403, nil)
	uw.do("POST", "/api/v1/applications/"+id+"/submit", nil, 403, nil)

	uw.do("POST", "/api/v1/applications/"+id+"/assign", nil, 200, nil)
	uw.do("POST", "/api/v1/applications/"+id+"/decision", map[string]any{"action": "REQUEST_DOCS",
		"comment": "Проверить обороты", "documents": []map[string]string{{"doc_type": "BANK_STATEMENT"}}}, 200, &v)
	if v.Application.Status != "DOCS_REQUESTED" || len(v.DocumentRequests) != 1 {
		t.Fatalf("status %s, requests %d", v.Application.Status, len(v.DocumentRequests))
	}
	// загрузка документа клиентом
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("request_id", v.DocumentRequests[0].ID)
	fw, _ := mw.CreateFormFile("file", "statement.csv")
	_, _ = fw.Write([]byte("date;amount\n2026-09-01;150000\n"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", base+"/api/v1/applications/"+id+"/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	cl.send(req, 200, &v)
	if v.Application.Status != "MANUAL_REVIEW" {
		t.Fatalf("after upload status %s, want MANUAL_REVIEW", v.Application.Status)
	}
	uw.do("POST", "/api/v1/applications/"+id+"/decision", map[string]any{"action": "COUNTER_OFFER",
		"amount": 1_200_000, "term_months": 24, "rate": 26, "comment": "Снижаем сумму из-за просрочек"}, 200, &v)
	if v.Application.Status != "COUNTER_OFFER" {
		t.Fatalf("status %s", v.Application.Status)
	}
	cl.do("POST", "/api/v1/applications/"+id+"/offer/accept", nil, 200, &v)
	cl.do("POST", "/api/v1/applications/"+id+"/sign", map[string]string{"code": v.SignCodeDemo}, 200, &v)
	waitFor(cl, id, "ISSUED")
}

func TestUnavailableSourceGoesToManual(t *testing.T) {
	c := login(t, "irina@tuman.demo")
	v := apply(c, "EXPRESS", "CASH_GAP", 2_000_000, 12, 24_000_000)
	if v.Application.Status != "MANUAL_REVIEW" {
		t.Fatalf("status %s, want MANUAL_REVIEW", v.Application.Status)
	}
}

func TestAccessControl(t *testing.T) {
	a := login(t, "anna@romashka.demo")
	var v view
	a.do("POST", "/api/v1/applications", map[string]any{"product_code": "EXPRESS", "purpose": "CASH_GAP"}, 201, &v)
	other := login(t, "dmitry@vector.demo")
	other.do("GET", "/api/v1/applications/"+v.Application.ID, nil, 404, nil)
	anon := &client{t: t}
	anon.do("GET", "/api/v1/applications", nil, 401, nil)
	// неполная заявка не отправляется
	a.do("POST", "/api/v1/applications/"+v.Application.ID+"/submit", nil, 422, nil)
	_ = fmt.Sprint()
}
