package handler

import (
	"net/http"
	"strconv"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// KBMaintenanceHandler exposes the knowledge-base maintenance endpoints.
//
// The three endpoints form one flow:
//
//	plan  — what would a backfill cost? (read-only, no embedding call)
//	run   — do it
//	runs  — how did it go?
//
// Authorization mirrors the rest of the KB surface: the route group carries
// the RBAC middleware (role + KB access level), and the handler re-resolves
// the KB access so a shared/agent-scoped caller cannot reach a KB it has no
// grant on.
type KBMaintenanceHandler struct {
	service interfaces.KBMaintenanceService
	// kbService is used to resolve the caller's access to the KB.
	kbService        interfaces.KnowledgeBaseService
	kbShareService   interfaces.KBShareService
	agentShareService interfaces.AgentShareService
}

// NewKBMaintenanceHandler creates a maintenance handler.
func NewKBMaintenanceHandler(
	service interfaces.KBMaintenanceService,
	kbService interfaces.KnowledgeBaseService,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
) *KBMaintenanceHandler {
	return &KBMaintenanceHandler{
		service:           service,
		kbService:         kbService,
		kbShareService:    kbShareService,
		agentShareService: agentShareService,
	}
}

// resolveKB resolves the KB and the caller's access to it.
func (h *KBMaintenanceHandler) resolveKB(c *gin.Context) (string, error) {
	id := secutils.SanitizeForLog(c.Param("id"))
	// Editor-level access: maintenance rewrites the knowledge base's index,
	// which is at least as sensitive as editing the KB itself.
	_, err := resolveHandlerKBAccessFor(
		c, id, h.kbService, h.kbShareService, h.agentShareService, types.OrgRoleEditor,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// PlanMaintenance godoc
// @Summary      预估知识库维护动作的成本
// @Description  只读预估，不修改任何数据。返回待索引 chunk 数、嵌入调用次数与预计耗时。
// @Tags         知识库维护
// @Accept       json
// @Produce      json
// @Param        id      path      string  true   "知识库ID"
// @Param        request body      types.KBMaintenancePlanRequest  true  "维护动作"
// @Success      200  {object}  map[string]interface{}  "预估结果"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "无权访问"
// @Failure      404  {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/maintenance/plan [post]
func (h *KBMaintenanceHandler) PlanMaintenance(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := h.resolveKB(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.KBMaintenancePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if req.Action != types.KBMaintenanceActionBackfillEmbeddings {
		c.Error(apperrors.NewBadRequestError(
			"unsupported action: " + req.Action + " (supported: " + types.KBMaintenanceActionBackfillEmbeddings + ")"))
		return
	}

	plan, err := h.service.PlanBackfillEmbeddings(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_base_id": id})
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan})
}

// RunMaintenance godoc
// @Summary      执行知识库维护动作
// @Description  目前只支持 backfill_embeddings：只给已有 chunk 补向量，不重建 chunk、不重蒸馏 Wiki、不调用 LLM。
// @Tags         知识库维护
// @Accept       json
// @Produce      json
// @Param        id      path      string  true   "知识库ID"
// @Param        request body      types.KBMaintenanceRunRequest  true  "维护动作"
// @Success      200  {object}  map[string]interface{}  "运行记录"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "无权访问"
// @Failure      404  {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/maintenance/run [post]
func (h *KBMaintenanceHandler) RunMaintenance(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := h.resolveKB(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.KBMaintenanceRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if req.Action != types.KBMaintenanceActionBackfillEmbeddings {
		c.Error(apperrors.NewBadRequestError(
			"unsupported action: " + req.Action + " (supported: " + types.KBMaintenanceActionBackfillEmbeddings + ")"))
		return
	}

	result, err := h.service.RunBackfillEmbeddings(ctx, id, &req)
	if err != nil {
		// An already-running backfill is a conflict, not a server fault —
		// the client should poll /maintenance/runs, not retry blindly.
		if strings.Contains(err.Error(), "already has a backfill running") {
			c.Error(apperrors.NewConflictError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_base_id": id})
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	// A real backfill keeps running after this response is written; 202
	// signals "accepted, poll the runs endpoint for progress".
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": result})
}

// ListMaintenanceRuns godoc
// @Summary      查询知识库维护记录
// @Description  返回最近的维护运行记录（进度、成本、错误数）。
// @Tags         知识库维护
// @Produce      json
// @Param        id    path   string  true   "知识库ID"
// @Param        limit query  int     false  "返回条数（默认 20）"
// @Success      200  {object}  map[string]interface{}  "运行记录列表"
// @Failure      403  {object}  errors.AppError         "无权访问"
// @Failure      404  {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/maintenance/runs [get]
func (h *KBMaintenanceHandler) ListMaintenanceRuns(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := h.resolveKB(c)
	if err != nil {
		c.Error(err)
		return
	}

	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if parsed, perr := strconv.Atoi(raw); perr == nil {
			limit = parsed
		}
	}

	runs, err := h.service.ListRuns(ctx, id, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_base_id": id})
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": runs})
}
