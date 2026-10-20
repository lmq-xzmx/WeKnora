package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DelegationTokenHandler implements HTTP handlers for delegation token management.
type DelegationTokenHandler struct {
	svc interfaces.DelegationTokenService
}

// NewDelegationTokenHandler creates a new DelegationTokenHandler
func NewDelegationTokenHandler(svc interfaces.DelegationTokenService) *DelegationTokenHandler {
	return &DelegationTokenHandler{svc: svc}
}

// createDelegationTokenRequest is the JSON body for POST /tenants/:id/delegation-tokens
type createDelegationTokenRequest struct {
	Name      string   `json:"name" binding:"required"`
	Scopes   []string `json:"scopes" binding:"required"`
	AllowedIPs []string `json:"allowed_ips"`
	ExpiresIn string   `json:"expires_in"` // e.g. "1h", "7d", "30d"
}

// CreateDelegationToken godoc
// @Summary      创建 Delegation Token
// @Description  租户 Owner/Admin 为第三方系统创建临时访问凭证
// @Tags         Delegation Token
// @Accept       json
// @Produce      json
// @Param        id   path int true "租户ID"
// @Param        body body createDelegationTokenRequest true "创建参数"
// @Success      201  {object} interfaces.DelegationTokenCreateResult
// @Failure      400  {object} errors.AppError
// @Failure      403  {object} errors.AppError
// @Router       /tenants/{id}/delegation-tokens [post]
func (h *DelegationTokenHandler) CreateDelegationToken(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid tenant id"))
		return
	}

	var req createDelegationTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewValidationError("invalid request: " + err.Error()))
		return
	}

	result, err := h.svc.CreateToken(ctx, interfaces.DelegationTokenCreateRequest{
		TenantID:   tenantID,
		Name:       req.Name,
		Scopes:     req.Scopes,
		AllowedIPs: req.AllowedIPs,
		ExpiresIn:  req.ExpiresIn,
		CreatedBy:  c.GetString("user_id"),
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to create delegation token: %v", err)
		c.Error(errors.NewInternalServerError("failed to create delegation token"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          result.Token.ID,
		"name":        result.Token.Name,
		"token":       result.Plain, // plain token only returned at creation time
		"scopes":      result.Token.Scopes,
		"allowed_ips": result.Token.AllowedIPs,
		"expires_at":  result.Token.ExpiresAt,
		"created_at":  result.Token.CreatedAt,
	})
}

// ListDelegationTokens godoc
// @Summary      列出 Delegation Tokens
// @Description  返回租户的所有 Delegation Token（不包含明文 token）
// @Tags         Delegation Token
// @Produce      json
// @Param        id path int true "租户ID"
// @Success      200 {array} types.DelegationToken
// @Router       /tenants/{id}/delegation-tokens [get]
func (h *DelegationTokenHandler) ListDelegationTokens(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid tenant id"))
		return
	}

	tokens, err := h.svc.ListTokens(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list delegation tokens: %v", err)
		c.Error(errors.NewInternalServerError("failed to list delegation tokens"))
		return
	}

	c.JSON(http.StatusOK, tokens)
}

// RevokeDelegationToken godoc
// @Summary      撤销 Delegation Token
// @Description  撤销指定的 Delegation Token
// @Tags         Delegation Token
// @Param        id         path int true "租户ID"
// @Param        tokenId    path int true "Token ID"
// @Success      204
// @Router       /tenants/{id}/delegation-tokens/{tokenId} [delete]
func (h *DelegationTokenHandler) RevokeDelegationToken(c *gin.Context) {
	ctx := c.Request.Context()

	tokenID, err := strconv.ParseUint(c.Param("tokenId"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("invalid token id"))
		return
	}

	if err := h.svc.RevokeToken(ctx, tokenID); err != nil {
		logger.Errorf(ctx, "Failed to revoke delegation token %d: %v", tokenID, err)
		c.Error(errors.NewInternalServerError("failed to revoke delegation token"))
		return
	}

	c.Status(http.StatusNoContent)
}
