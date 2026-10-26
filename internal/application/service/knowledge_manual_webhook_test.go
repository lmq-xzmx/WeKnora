package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/integration/geoknowledge"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// 手工知识在线编辑 → 必须通知 GEO-Knowledge
// ============================================================================
//
// 缺口（#17）：UpdateManualKnowledge 此前**不发** webhook —— 用户在
// WeKnora 里改手工知识，GEO-K 完全收不到，要靠整库拉取才发现变化。
// 修复加在 a49f3c10，但**没有测试钉住**：谁哪天把 notify 删了，
// 没有任何东西会响。
//
// 这条测试从「HTTP 真的发出去了」的角度验，不看内部调用：
// 起一个本地 server 当 GEO-K，断言收到了什么。
// ============================================================================

// webhookCapture 收下 WeKnora 发来的 webhook。
type webhookCapture struct {
	mu       sync.Mutex
	payloads []geoknowledge.Payload
	headers  []http.Header
}

// server 起一个假 GEO-K，返回 200。
func (c *webhookCapture) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p geoknowledge.Payload
		if err := json.Unmarshal(body, &p); err == nil {
			c.mu.Lock()
			c.payloads = append(c.payloads, p)
			c.headers = append(c.headers, r.Header.Clone())
			c.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitFor 轮询等条件成立，最多等 3 秒。
//
// webhook 是异步 goroutine 发的，调用返回时请求可能还没到 —— 这里等一下。
func (c *webhookCapture) waitFor(t *testing.T, pred func([]geoknowledge.Payload) bool) []geoknowledge.Payload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		snapshot := append([]geoknowledge.Payload(nil), c.payloads...)
		c.mu.Unlock()
		if pred(snapshot) {
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]geoknowledge.Payload(nil), c.payloads...)
}

// manualTaskStub 记下入队的 asynq 任务，不真的跑。
type manualTaskStub struct {
	enqueued int
}

func (s *manualTaskStub) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	s.enqueued++
	return &asynq.TaskInfo{ID: "task-1"}, nil
}

// manualWebhookFixture 造一个能跑 UpdateManualKnowledge 的最小环境。
type manualWebhookFixture struct {
	svc      *knowledgeService
	repo     *documentKnowledgeSpy
	kbs      *documentKBLookup
	tasks    *manualTaskStub
	capture  *webhookCapture
	server   *httptest.Server
	tenantID uint64
}

func newManualWebhookFixture(t *testing.T) *manualWebhookFixture {
	t.Helper()

	f := newDocumentWriteFixture(t)

	// 手工知识得有 metadata.content，否则正文取不到（knowledgeBodyForWebhook）。
	meta := types.NewManualKnowledgeMetadata("原始正文", types.ManualKnowledgeStatusDraft, 1)
	metaJSON, err := meta.ToJSON()
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
		Update("metadata", metaJSON).Error)

	capture := &webhookCapture{}
	srv := capture.server(t)

	f.svc.geoKWebhook = geoknowledge.NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL: srv.URL,
		Enabled:    true,
	})
	tasks := &manualTaskStub{}
	f.svc.task = tasks

	return &manualWebhookFixture{
		svc:      f.svc,
		repo:     f.repo,
		kbs:      f.kbs,
		tasks:    tasks,
		capture:  capture,
		server:   srv,
		tenantID: 7,
	}
}

// writeCtx 造一个能通过写权限检查的上下文。
func (f *manualWebhookFixture) writeCtx(t *testing.T) context.Context {
	t.Helper()
	ctx := types.WithExecutionTenant(
		types.WithCaller(context.Background(), types.Caller{
			TenantID: f.tenantID, UserID: "editor", Role: types.TenantRoleAdmin,
		}),
		f.tenantID,
	)
	grant := &access.KBAccess{
		KnowledgeBase:     f.kbs.values["kb"],
		Caller:            types.CallerFromContext(ctx),
		EffectiveTenantID: f.tenantID,
		Permission:        types.OrgRoleEditor,
	}
	ctx = grant.Context(ctx)
	return context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: f.tenantID})
}

// TestUpdateManualKnowledge_NotifiesGEOK 改草稿要发 updated 事件。
func TestUpdateManualKnowledge_NotifiesGEOK(t *testing.T) {
	f := newManualWebhookFixture(t)
	ctx := f.writeCtx(t)

	_, err := f.svc.UpdateManualKnowledge(ctx, "doc", &types.ManualKnowledgePayload{
		Title:   "改过的标题",
		Content: "改过的正文",
		Status:  types.ManualKnowledgeStatusDraft,
	})
	require.NoError(t, err)

	payloads := f.capture.waitFor(t, func(ps []geoknowledge.Payload) bool { return len(ps) >= 1 })
	require.Len(t, payloads, 1, "改手工知识后应该发一条 webhook")

	p := payloads[0]
	require.Equal(t, geoknowledge.EventUpdated, p.Event, "事件类型应该是 updated")
	require.Equal(t, string(geoknowledge.EntityKnowledge), p.Entity)
	require.Equal(t, "doc", p.ID, "weknora_id 必须是这条知识的 ID")
	require.Equal(t, "改过的标题", p.Title)
	require.Contains(t, p.Content, "改过的正文", "载荷里要带上新正文")
	require.Equal(t, "kb", p.KnowledgeBaseID, "载荷里要带上知识库 ID，GEO-K 才知道落到哪")

	// 正文完整（手工知识的正文在 metadata.content 里，是完整的）。
	require.NotNil(t, p.ContentComplete, "content_complete 不能缺省")
	require.True(t, *p.ContentComplete, "手工知识的正文是完整的")
}

// TestUpdateManualKnowledge_NotifiesGEOK_NotRecreated 必须是 updated 不是 created。
//
// GEO-K 收到 created 会去「新建」一条本地记录；收到 updated 才会去比对
// 基线、判冲突。事件类型发错，用户的条目会被复制一份。
func TestUpdateManualKnowledge_NotifiesGEOK_NotRecreated(t *testing.T) {
	f := newManualWebhookFixture(t)
	ctx := f.writeCtx(t)

	_, err := f.svc.UpdateManualKnowledge(ctx, "doc", &types.ManualKnowledgePayload{
		Content: "新正文",
		Status:  types.ManualKnowledgeStatusDraft,
	})
	require.NoError(t, err)

	payloads := f.capture.waitFor(t, func(ps []geoknowledge.Payload) bool { return len(ps) >= 1 })
	require.Len(t, payloads, 1)
	require.NotEqual(t, geoknowledge.EventCreated, payloads[0].Event,
		"改已有条目不能发 created —— GEO-K 会当成新条目复制一份")
}

// TestUpdateManualKnowledge_NotifiesGEOK_Publish 发布路径也要发。
//
// 草稿和发布是两条分支，只给一条加通知等于只修了一半 ——
// 用户「保存并发布」是更常用的路径。
func TestUpdateManualKnowledge_NotifiesGEOK_Publish(t *testing.T) {
	f := newManualWebhookFixture(t)
	ctx := f.writeCtx(t)

	_, err := f.svc.UpdateManualKnowledge(ctx, "doc", &types.ManualKnowledgePayload{
		Title:   "发布的标题",
		Content: "发布的正文",
		Status:  types.ManualKnowledgeStatusPublish,
	})
	require.NoError(t, err)
	require.Equal(t, 1, f.tasks.enqueued, "发布路径应该入队了一个异步处理任务")

	payloads := f.capture.waitFor(t, func(ps []geoknowledge.Payload) bool { return len(ps) >= 1 })
	require.Len(t, payloads, 1, "发布路径也要发 webhook")
	require.Equal(t, geoknowledge.EventUpdated, payloads[0].Event)
	require.Contains(t, payloads[0].Content, "发布的正文")
}

// TestUpdateManualKnowledge_NotifiesGEOK_Signed 通知必须带签名。
//
// GEO-K 侧是 fail-closed 验签的：没签名头一律 401。
// 少了签名头，通知会被 GEO-K 全量拒收 —— 症状是「WeKnora 明明改了，
// GEO-K 就是没反应」。
func TestUpdateManualKnowledge_NotifiesGEOK_Signed(t *testing.T) {
	f := newManualWebhookFixture(t)
	f.svc.geoKWebhook = geoknowledge.NewClient(&config.GEOKnowledgeWebhookConfig{
		WebhookURL:    f.server.URL,
		Enabled:       true,
		WebhookSecret: "signing-key",
	})
	ctx := f.writeCtx(t)

	_, err := f.svc.UpdateManualKnowledge(ctx, "doc", &types.ManualKnowledgePayload{
		Content: "带签名的正文",
		Status:  types.ManualKnowledgeStatusDraft,
	})
	require.NoError(t, err)

	_ = f.capture.waitFor(t, func(ps []geoknowledge.Payload) bool { return len(ps) >= 1 })

	f.capture.mu.Lock()
	defer f.capture.mu.Unlock()
	require.NotEmpty(t, f.capture.headers, "没收到任何请求")
	sig := f.capture.headers[0].Get("X-Webhook-Secret")
	require.NotEmpty(t, sig, "通知必须带 X-Webhook-Secret 签名头，否则 GEO-K 会全量 401")
}
