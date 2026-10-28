package geoknowledge

import (
	"context"
	"encoding/json"
	"io"
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
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil); err != nil {
		t.Fatalf("disabled client should not error, got %v", err)
	}
}

func TestNotifyNoURL(t *testing.T) {
	c := NewClient(&config.GEOKnowledgeWebhookConfig{Enabled: true})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil); err != nil {
		t.Fatalf("no-URL client should not error, got %v", err)
	}
}

func TestNotifySuccess(t *testing.T) {
	var gotAuth, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSecret = r.Header.Get("X-Webhook-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL:    srv.URL,
		Enabled:       true,
		Token:         "secret-token",
		WebhookSecret: "hmac-key",
	})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if gotSecret == "" {
		t.Error("X-Webhook-Signature header missing")
	}
}

// ---------------------------------------------------------------------------
// HMAC 签名
// ---------------------------------------------------------------------------
//
// 签名算法在 WeKnora（这里）和 GEO-K（那边）各实现了一遍。
// 两边只要有一处不一致，症状是「WeKnora 照发签名，GEO-K 照拒」，
// 日志里只看得到 401 和「签名不匹配」，根本看不出是哪边算错了。
//
// 所以这里用**已知向量**把算法钉死：固定的 key + 固定的 body → 固定的签名。
// 这个常量是独立算出来的（不是拿本函数的输出反过来写进测试的），
// 所以只要算法被改动（换了哈希、换了编码方式、签成了 body 的副本而不是 body），
// 测试立刻失败。
const (
	// knownVectorKey 是已知向量用的密钥。
	knownVectorKey = "known-vector-key"
	// knownVectorBody 是已知向量用的请求体原文。
	knownVectorBody = `{"event":"created","entity_type":"knowledge","weknora_id":"kn-1"}`
	// knownVectorSignature 是上面两者算出的 HMAC-SHA256 hex。
	//
	//	python3 -c "import hmac,hashlib;print(hmac.new(b'known-vector-key', \
	//	  b'{\"event\":\"created\",\"entity_type\":\"knowledge\",\"weknora_id\":\"kn-1\"}', \
	//	  hashlib.sha256).hexdigest())"
	knownVectorSignature = "4123a36742ce72ebb3fb70621a8238328042a93a625ea3b3750e4c24e3f2595f"
)

// TestHMACSHA256_KnownVector 确认 hmacSHA256 对已知输入产出已知签名。
func TestHMACSHA256_KnownVector(t *testing.T) {
	got := hmacSHA256([]byte(knownVectorBody), knownVectorKey)
	if got != knownVectorSignature {
		t.Fatalf("签名不匹配:\n got = %s\nwant = %s", got, knownVectorSignature)
	}
}

// TestNotifySignatureMatchesBody 确认发出去的签名头确实是对**这次实际发出的 body** 签的。
//
// 上一版只断言了「头非空」—— 那对「签名头是拿别的东西算的」完全无效。
// GEO-K 是拿收到的 body 原文重算比对的，所以这里必须从服务端侧
// 把真实收到的 body 抓下来重算，逐字节比对。
//
// 这个断言锁的是最容易出错的一处：签名算的是 body A、发出去的是 body B
// （比如中间做了 JSON 重新序列化，或者重试时 reader 没复位）。
func TestNotifySignatureMatchesBody(t *testing.T) {
	const secret = "signature-match-secret"

	var gotSecret string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("X-Webhook-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL:    srv.URL,
		Enabled:       true,
		WebhookSecret: secret,
	})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "kn-1", "t", "c", "", "", "", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(gotBody) == 0 {
		t.Fatal("服务端没收到 body")
	}
	if want := "sha256=" + hmacSHA256(gotBody, secret); gotSecret != want {
		t.Errorf("X-Webhook-Signature = %q，期望 %q（body=%s）", gotSecret, want, gotBody)
	}
}

// TestNotifySignatureOverRawBody 确认签名算的是**原始字节**，而不是解析后的结构体。
//
// 载荷里含 JSON 里天然会转义的字符（引号、换行、反斜杠）。
// 如果哪天有人改成「先 Unmarshal 再 Marshal 一次来算签名」，
// 转义规则一变签名就对不上，GEO-K 全量 401 —— 而症状仍然只是「签名不匹配」。
// 这个测试用带转义字符的正文把那条路堵死。
func TestNotifySignatureOverRawBody(t *testing.T) {
	const secret = "raw-body-secret"

	var gotSecret string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("X-Webhook-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 正文里带引号 / 换行 / 反斜杠 —— JSON 序列化后一定和原字符串不一样
	content := "第一行 \"引号\" 和 \\ 反斜杠\n第二行"
	c := NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL:    srv.URL,
		Enabled:       true,
		WebhookSecret: secret,
	})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "kn-1", "t", content, "", "", "", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if want := "sha256=" + hmacSHA256(gotBody, secret); gotSecret != want {
		t.Errorf("X-Webhook-Signature = %q，期望 %q（body=%s）", gotSecret, want, gotBody)
	}
}

// TestNotifyNoSecretNoHeader 确认没配 secret 时**不发**签名头。
//
// 反过来（配了 secret 却漏发）GEO-K 会全量 401；
// 但「没配 secret 却发了空签名头」同样会让 GEO-K 拒绝 ——
// 早失败比到联调时才发现要好。
func TestNotifyNoSecretNoHeader(t *testing.T) {
	var headerPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, headerPresent = r.Header["X-Webhook-Signature"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL: srv.URL,
		Enabled:    true,
	})
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "kn-1", "t", "c", "", "", "", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if headerPresent {
		t.Error("未配置 secret 时不应发送 X-Webhook-Signature 头")
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
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil); err != nil {
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
	err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil)
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
	if err := c.Notify(context.Background(), EventCreated, EntityKnowledge, "id", "t", "c", "", "", "", nil); err == nil {
		t.Fatal("expected error when all attempts fail")
	}
	if n := atomic.LoadInt32(&calls); n != maxAttempts {
		t.Errorf("server called %d times, want %d", n, maxAttempts)
	}
}

// 载荷必须如实带 channel 和 content_complete —— GEO-K 靠它们区分
// 「我们推的（api）」和「用户建的（web）」，以及「完整正文」和「AI 摘要」。
// 这两个字段缺了，GEO-K 就只能靠推断，推断错一次就是数据损坏或假冲突。
func TestNotifySendsChannelAndContentComplete(t *testing.T) {
	var payload Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	complete := true
	c := newTestClient(srv.URL)
	err := c.Notify(context.Background(), EventUpdated, EntityKnowledge,
		"kn-1", "t", "c", "", "https://example.com", "api", &complete, "kb-1")
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if payload.Channel != "api" {
		t.Errorf("Channel = %q, want api", payload.Channel)
	}
	if payload.ContentComplete == nil || !*payload.ContentComplete {
		t.Errorf("ContentComplete = %v, want true", payload.ContentComplete)
	}
	if payload.KnowledgeBaseID != "kb-1" {
		t.Errorf("KnowledgeBaseID = %q, want kb-1", payload.KnowledgeBaseID)
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
	_ = c.Notify(context.Background(), EventCreated, EntityKnowledge, "kn-1", "t", "c", "", "", "", nil)

	if len(bodies) != maxAttempts {
		t.Fatalf("got %d requests, want %d", len(bodies), maxAttempts)
	}
	for i, b := range bodies {
		if b == "" {
			t.Errorf("attempt %d sent empty body", i)
		}
	}
}
