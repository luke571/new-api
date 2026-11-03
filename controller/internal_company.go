package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

func InternalSelf(c *gin.Context) {
	result, err := model.InternalSummary(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func InternalOverview(c *gin.Context) {
	var teams []model.InternalTeam
	var invites []model.InternalInvite
	var members []model.InternalMember
	if err := model.DB.Order("id desc").Limit(200).Find(&teams).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Order("id desc").Limit(200).Find(&invites).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Order("id desc").Limit(1000).Find(&members).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"teams": teams, "invites": invites, "members": members, "enabled": model.InternalModeEnabled()})
}
func InternalCreateInvite(c *gin.Context) {
	invite, token, err := model.NewInternalInvite(c.GetInt("id"), 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	recordManageAudit(c, "internal.invite.create", map[string]any{"invite_id": invite.ID})
	common.ApiSuccess(c, gin.H{"invite": invite, "token": token})
}
func InternalRevokeInvite(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err = model.DB.Model(&model.InternalInvite{}).Where("id = ? AND used_at = 0", id).Update("expires_at", model.GetDBTimestamp()).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "internal.invite.revoke", map[string]any{"invite_id": id})
	common.ApiSuccess(c, nil)
}
func InternalRecoveryInvite(c *gin.Context) {
	var input struct {
		UserID int `json:"user_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil || input.UserID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if requireAdminUserProof(c, service.VerificationScopeAdminUserUpdate, service.AdminUserContext{UserID: input.UserID}) == nil {
		return
	}
	invite, token, err := model.NewInternalInvite(c.GetInt("id"), input.UserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	recordManageAudit(c, "internal.recovery.create", map[string]any{"user_id": input.UserID, "invite_id": invite.ID})
	common.ApiSuccess(c, gin.H{"token": token, "expires_at": invite.ExpiresAt})
}
func InternalResetPassword(c *gin.Context) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.ResetInternalPassword(input.Token, input.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": common.NewMessage("Invitation or recovery request is invalid or expired")})
		return
	}
	common.ApiSuccess(c, nil)
}
func InternalCreateTeam(c *gin.Context) {
	var input struct {
		Name    string `json:"name"`
		OwnerID int    `json:"owner_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	team, err := model.CreateInternalTeam(input.Name, input.OwnerID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "internal.team.create", map[string]any{"team_id": team.ID, "owner_id": team.OwnerID})
	common.ApiSuccess(c, team)
}
func InternalUpdateMember(c *gin.Context) {
	var input struct {
		TeamID     int   `json:"team_id"`
		UserID     int   `json:"user_id"`
		QuotaLimit int64 `json:"quota_limit"`
		Active     bool  `json:"active"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	root := c.GetInt("role") >= common.RoleRootUser
	if err := model.SetInternalMember(c.GetInt("id"), input.TeamID, input.UserID, input.QuotaLimit, input.Active, root); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "internal.member.update", map[string]any{"team_id": input.TeamID, "user_id": input.UserID, "active": input.Active})
	common.ApiSuccess(c, nil)
}
func InternalGrantAddon(c *gin.Context) {
	var input struct {
		SubscriptionID int    `json:"subscription_id"`
		Tier           string `json:"tier"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	tiers := map[string]int64{"small": 5000000, "medium": 15000000, "large": 30000000}
	amount, ok := tiers[input.Tier]
	if !ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	addon, err := model.GrantInternalAddon(input.SubscriptionID, amount, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "internal.addon.grant", map[string]any{"addon_id": addon.ID, "subscription_id": addon.SubscriptionID, "tier": input.Tier})
	common.ApiSuccess(c, addon)
}
func InternalUsage(c *gin.Context) {
	var charges []model.InternalCharge
	if err := model.DB.Where("user_id = ?", c.GetInt("id")).Order("created_at desc").Limit(100).Find(&charges).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, charges)
}
func InternalTeamUsage(c *gin.Context) {
	owner, member, err := model.InternalOwner(model.DB, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if member == nil || owner != c.GetInt("id") {
		c.Status(http.StatusForbidden)
		return
	}
	var charges []model.InternalCharge
	if err = model.DB.Where("team_id = ?", member.TeamID).Order("created_at desc").Limit(100).Find(&charges).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, charges)
}
