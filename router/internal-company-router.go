package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func registerInternalCompanyRoutes(api *gin.RouterGroup) {
	api.POST("/internal/reset-password", middleware.AnonymousRequestBodyLimit(), middleware.CriticalRateLimit(), controller.InternalResetPassword)
	user := api.Group("/internal", middleware.UserAuth())
	user.GET("/self", controller.InternalSelf)
	user.GET("/usage", controller.InternalUsage)
	user.POST("/members", middleware.CriticalRateLimit(), controller.InternalUpdateMember)
	user.GET("/team-usage", controller.InternalTeamUsage)
	admin := api.Group("/internal/admin", middleware.RootAuth())
	admin.GET("/overview", controller.InternalOverview)
	admin.POST("/invites", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.InternalCreateInvite)
	admin.DELETE("/invites/:id", middleware.CriticalRateLimit(), controller.InternalRevokeInvite)
	admin.POST("/recovery", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.InternalRecoveryInvite)
	admin.POST("/teams", middleware.CriticalRateLimit(), controller.InternalCreateTeam)
	admin.POST("/addons", middleware.CriticalRateLimit(), controller.InternalGrantAddon)
}
