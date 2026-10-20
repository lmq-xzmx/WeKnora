package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TenantDomainHandler implements HTTP request handlers for tenant domain mapping management (Plan D)
type TenantDomainHandler struct {
	domainSvc interfaces.TenantDomainService
	tenantSvc interfaces.TenantService
}

// NewTenantDomainHandler creates a new TenantDomainHandler
func NewTenantDomainHandler(
	domainSvc interfaces.TenantDomainService,
	tenantSvc interfaces.TenantService,
) *TenantDomainHandler {
	return &TenantDomainHandler{
		domainSvc: domainSvc,
		tenantSvc: tenantSvc,
	}
}

// createDomainRequest is the JSON body for POST /tenants/:id/domains
type createDomainRequest struct {
	Domain string `json:"domain" binding:"required,fqdn"`
}

// CreateDomain godoc
// @Summary      创建租户域名映射（Plan D）
// @Description  为指定租户添加一个域名映射，用于多租户 OIDC 动态路由
// @Tags         租户域名管理
// @Accept       json
// @Produce      json
// @Param        id   path int true "租户ID"
// @Param        body body createDomainRequest true "域名"
// @Success      201  {object} types.TenantDomain
// @Failure      400  {object} errors.AppError
// @Failure      403  {object} errors.AppError
// @Failure      404  {object} errors.AppError
// @Router       /tenants/{id}/domains [post]
func (h *TenantDomainHandler) CreateDomain(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid tenant id"))
		return
	}

	// Verify tenant exists
	tenant, err := h.tenantSvc.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		c.Error(errors.NewNotFoundError("tenant not found"))
		return
	}

	var req createDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewValidationError("invalid request: " + err.Error()))
		return
	}

	domain := &types.TenantDomain{
		TenantID: tenantID,
		Domain:   req.Domain,
		IsActive: true,
	}

	if err := h.domainSvc.Create(ctx, domain); err != nil {
		logger.Errorf(ctx, "Failed to create tenant domain: %v", err)
		c.Error(errors.NewInternalServerError("failed to create domain mapping"))
		return
	}

	c.JSON(http.StatusCreated, domain)
}

// DeleteDomain godoc
// @Summary      删除租户域名映射（Plan D）
// @Description  删除指定租户的域名映射
// @Tags         租户域名管理
// @Produce      json
// @Param        id       path int true "租户ID"
// @Param        domainId path int true "域名映射ID"
// @Success      204
// @Failure      403  {object} errors.AppError
// @Failure      404  {object} errors.AppError
// @Router       /tenants/{id}/domains/{domainId} [delete]
func (h *TenantDomainHandler) DeleteDomain(c *gin.Context) {
	ctx := c.Request.Context()

	domainID, err := strconv.ParseUint(c.Param("domainId"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid domain id"))
		return
	}

	if err := h.domainSvc.Delete(ctx, domainID); err != nil {
		logger.Errorf(ctx, "Failed to delete tenant domain %d: %v", domainID, err)
		c.Error(errors.NewInternalServerError("failed to delete domain mapping"))
		return
	}

	c.Status(http.StatusNoContent)
}

// ListDomains godoc
// @Summary      列出租户的所有域名映射（Plan D）
// @Description  返回指定租户关联的所有域名
// @Tags         租户域名管理
// @Produce      json
// @Param        id path int true "租户ID"
// @Success      200 {array} types.TenantDomain
// @Failure      404 {object} errors.AppError
// @Router       /tenants/{id}/domains [get]
func (h *TenantDomainHandler) ListDomains(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid tenant id"))
		return
	}

	// Verify tenant exists
	tenant, err := h.tenantSvc.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		c.Error(errors.NewNotFoundError("tenant not found"))
		return
	}

	domains, err := h.domainSvc.ListByTenantID(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list tenant domains: %v", err)
		c.Error(errors.NewInternalServerError("failed to list domain mappings"))
		return
	}

	c.JSON(http.StatusOK, domains)
}

// GetDomain godoc
// @Summary      查询域名映射（Plan D）
// @Description  根据域名查询对应的租户和映射信息
// @Tags         租户域名管理
// @Produce      json
// @Param        domain query string true "域名"
// @Success      200 {object} getDomainResponse
// @Failure      404 {object} errors.AppError
// @Router       /tenants/domains/lookup [get]
func (h *TenantDomainHandler) GetDomain(c *gin.Context) {
	ctx := c.Request.Context()
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		c.Error(errors.NewValidationError("domain is required"))
		return
	}

	tenant, err := h.domainSvc.GetTenantByDomain(ctx, domain)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant by domain %s: %v", domain, err)
		c.Error(errors.NewInternalServerError("failed to lookup domain"))
		return
	}
	if tenant == nil {
		c.Error(errors.NewNotFoundError("domain not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tenant_id": tenant.ID,
		"tenant":    tenant,
	})
}
