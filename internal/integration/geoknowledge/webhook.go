// Package geoknowledge provides WeKnora → GEO-Knowledge outbound webhook integration.
package geoknowledge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
)

// maxAttempts 是 webhook 投递的最大尝试次数（含首次）。
// 3 次 = 首次 + 2 次重试，退避 200ms / 400ms，最坏多花约 0.6s。
// 调用方是异步 goroutine，不会拖慢用户请求。
const maxAttempts = 3

// EventType represents the webhook event type.
type EventType string

const (
	EventCreated EventType = "created"
	EventUpdated EventType = "updated"
	EventDeleted EventType = "deleted"
)

// EntityType represents the type of entity being synchronized.
type EntityType string

const (
	EntityKnowledge EntityType = "knowledge"
	EntityArticle  EntityType = "article"
)

// Payload is the JSON payload sent to GEO-Knowledge on knowledge changes.
type Payload struct {
	Event     EventType `json:"event"`
	Entity    string    `json:"entity_type"`
	ID        string    `json:"weknora_id"`
	Title     string    `json:"title,omitempty"`
	Content   string    `json:"content,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	SourceURL string    `json:"source_url,omitempty"`
	// KnowledgeBaseID 是这条知识所属的 WeKnora 知识库 id。
	// GEO-K 靠它把回流的条目落到正确的本地基地 —— 缺了这个字段，
	// GEO-K 只能建一条 kb_id 为空的孤儿行，在知识库列表里根本看不到。
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	Timestamp       string `json:"timestamp"`
}

// Client is the WeKnora → GEO-Knowledge webhook client.
type Client struct {
	cfg     *config.GEOKnowledgeWebhookConfig
	httpCli *http.Client
}

// NewClient creates a new GEO-Knowledge webhook client.
func NewClient(cfg *config.GEOKnowledgeWebhookConfig) *Client {
	return &Client{
		cfg: cfg,
		httpCli: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Notify sends a webhook notification to GEO-Knowledge.
// It is idempotent — nil cfg or disabled config logs and returns nil silently.
//
// knowledgeBaseID 可选：传了就带上，GEO-K 能把条目落到正确的本地基地；
// 不传行为跟以前一样（GEO-K 会回落到默认解析）。
func (c *Client) Notify(ctx context.Context, event EventType, entity EntityType, id, title, content, summary, sourceURL string, knowledgeBaseID ...string) error {
	if c == nil || c.cfg == nil || !c.cfg.Enabled {
		return nil // Disabled, nothing to do
	}
	if c.cfg.WebhookURL == "" {
		return nil // Not configured, nothing to do
	}

	payload := Payload{
		Event:     event,
		Entity:    string(entity),
		ID:        id,
		Title:     title,
		Content:   content,
		Summary:   summary,
		SourceURL: sourceURL,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if len(knowledgeBaseID) > 0 {
		payload.KnowledgeBaseID = knowledgeBaseID[0]
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("geoknowledge webhook: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("geoknowledge webhook: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// GEO-Knowledge 的 webhook 路由组挂了 CasdoorJWTOrAPIKey 中间件，
	// 服务间调用必须带 Bearer 凭证（其 GEO_FLOW_API_KEY）。
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	// Sign with HMAC-SHA256 if secret is configured
	if c.cfg.WebhookSecret != "" {
		sig := hmacSHA256(body, c.cfg.WebhookSecret)
		req.Header.Set("X-Webhook-Secret", sig)
	}

	// 重试：网络抖动或 GEO-K 短暂不可用时，单次失败会永久丢事件。
	// 只对可重试的情况重试（网络错误 / 5xx / 429）；4xx 是请求本身的问题，
	// 重试也不会变好，直接失败。
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// 指数退避：200ms / 400ms
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			}
			// 每次重试都要重建 body（第一次的 reader 已被读完）
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := c.httpCli.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("geoknowledge webhook: do request: %w", err)
			continue
		}
		status := resp.StatusCode
		// 读掉并丢弃响应体，让连接可以复用
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if status >= 200 && status < 300 {
			return nil
		}
		lastErr = fmt.Errorf("geoknowledge webhook: unexpected status %d", status)
		if status < 500 && status != http.StatusTooManyRequests {
			// 4xx（除 429）：请求本身有问题，重试无意义
			break
		}
	}

	return lastErr
}

// hmacSHA256 returns the HMAC-SHA256 hex string of data with key.
func hmacSHA256(data []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
