package geoknowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
)

func newTestClient(url string) *Client {
	return NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL: url,
		Enabled:    true,
	})
}

func TestNotifyDisabled(t *testing.T) {
	c := NewClient(&config.GEOKnowledgeWebhookConfig{WebhookURL: "http://127.0.0.1:1", Enabled: false})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", ""); err != nil {
		t.Fatalf("disabled client should not error, got %v", err)
	}
}

func TestNotifyNoURL(t *testing.T) {
	c := NewClient(&config.GEOKnowledgeWebhookConfig{Enabled: true})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", ""); err != nil {
		t.Fatalf("no-URL client should not error, got %v", err)
	}
}

func TestNotifySuccess(t *testing.T) {
	var gotAuth, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSecret = r.Header.Get("X-Webhook-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL:    srv.URL,
		Enabled:       true,
		Token:         "secret-token",
		WebhookSecret: "hmac-key",
	})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", ""); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if gotSecret == "" {
		t.Error("X-Webhook-Secret header missing")
	}
}

// 服务端 5xx 时应重试并最终成功。
func TestNotifyRetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", ""); err != nil {
		t.Fatalf("Notify should succeed after retries: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("server called %d times, want 3", n)
	}
}

// 4xx（除 429）不应重试 —— 请求本身有问题，重试也没用。
func TestNotifyDoesNotRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("server called %d times, want 1 (no retry on 4xx)", n)
	}
}

// 全部尝试都失败时应返回错误。
func TestNotifyFailsAfterMaxAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", ""); err == nil {
		t.Fatal("expected error when all attempts fail")
	}
	if n := atomic.LoadInt32(&calls); n != maxAttempts {
		t.Errorf("server called %d times, want %d", n, maxAttempts)
	}
}

// 重试时 body 必须能被重新发送（第一次的 reader 已读完）。
func TestNotifyResendsBodyOnRetry(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_ = c.Notify(context.Background(), EventCreated, EntityKnowledge, "kn-1", "t", "c", "", "")

	if len(bodies) != maxAttempts {
		t.Fatalf("got %d requests, want %d", len(bodies), maxAttempts)
	}
	for i, b := range bodies {
		if b == "" {
			t.Errorf("attempt %d sent empty body", i)
		}
	}
}
