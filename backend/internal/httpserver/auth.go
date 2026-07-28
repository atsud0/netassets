package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"netassets/internal/auth"
	"netassets/internal/config"
)

const principalContextKey = "authenticated_principal"

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func registerAuthRoutes(api *gin.RouterGroup, cfg config.Config, service *auth.Service, logger *slog.Logger) gin.HandlerFunc {
	authGroup := api.Group("/auth")
	authGroup.POST("/login", func(c *gin.Context) {
		var request loginRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请输入用户名和密码")
			return
		}
		result, err := service.Login(c.Request.Context(), auth.LoginInput{
			Username:  request.Username,
			Password:  request.Password,
			IPAddress: c.ClientIP(),
			UserAgent: c.Request.UserAgent(),
			RequestID: c.GetString("request_id"),
		})
		if errors.Is(err, auth.ErrUnauthorized) {
			writeAPIError(c, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
			return
		}
		if err != nil {
			logger.Error("login failed", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "登录服务暂不可用")
			return
		}
		setSessionCookie(c, cfg, result.Token, result.ExpiresAt)
		c.JSON(http.StatusOK, gin.H{"user": result.User, "expires_at": result.ExpiresAt})
	})

	requireAuth := authenticationMiddleware(cfg, service, logger)
	requireSession := sessionAuthenticationMiddleware()
	authGroup.GET("/me", requireAuth, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": mustPrincipal(c)})
	})
	authGroup.GET("/profile", requireAuth, requireSession, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": mustPrincipal(c)})
	})
	authGroup.PUT("/profile", requireAuth, requireSession, func(c *gin.Context) {
		var input auth.ProfileInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "个人资料格式不正确")
			return
		}
		user, err := service.UpdateProfile(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, auth.ErrInvalid) {
			writeAPIError(c, http.StatusBadRequest, "invalid_profile", "请填写有效的显示名称和联系方式")
			return
		}
		if errors.Is(err, auth.ErrUnauthorized) {
			clearSessionCookie(c, cfg)
			writeAPIError(c, http.StatusUnauthorized, "unauthorized", "登录状态已失效，请重新登录")
			return
		}
		if err != nil {
			logger.Error("update profile", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "更新个人资料失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": user})
	})
	authGroup.PUT("/profile/password", requireAuth, requireSession, func(c *gin.Context) {
		var input auth.ChangePasswordInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "密码格式不正确")
			return
		}
		err := service.ChangePassword(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, auth.ErrInvalid) {
			writeAPIError(c, http.StatusBadRequest, "invalid_password", "当前密码不能为空，新密码至少需要 10 位")
			return
		}
		if errors.Is(err, auth.ErrUnauthorized) {
			writeAPIError(c, http.StatusForbidden, "current_password_incorrect", "当前密码不正确")
			return
		}
		if err != nil {
			logger.Error("change password", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "修改密码失败")
			return
		}
		c.Status(http.StatusNoContent)
	})
	authGroup.GET("/sessions", requireAuth, requireSession, func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchSessions(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list login sessions", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "获取登录设备失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	authGroup.DELETE("/sessions/others", requireAuth, requireSession, func(c *gin.Context) {
		count, err := service.RevokeOtherSessions(c.Request.Context(), mustPrincipal(c), c.GetString("request_id"), c.ClientIP())
		if err != nil {
			logger.Error("revoke other login sessions", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "退出其他登录设备失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"revoked_count": count})
	})
	authGroup.DELETE("/sessions/:id", requireAuth, requireSession, func(c *gin.Context) {
		err := service.RevokeSession(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, auth.ErrCurrentSession):
			writeAPIError(c, http.StatusBadRequest, "current_session_cannot_be_revoked", "当前设备请通过退出登录操作退出")
		case errors.Is(err, auth.ErrSessionNotFound):
			writeAPIError(c, http.StatusNotFound, "session_not_found", "登录设备不存在或已退出")
		case err != nil:
			logger.Error("revoke selected login session", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "退出登录设备失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})
	authGroup.PUT("/current-organization", requireAuth, requireSession, func(c *gin.Context) {
		var request struct {
			OrganizationID string `json:"organization_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择当前工作组织")
			return
		}
		user, err := service.SwitchOrganization(c.Request.Context(), mustPrincipal(c), request.OrganizationID, c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, auth.ErrOrganizationForbidden) {
			writeAPIError(c, http.StatusForbidden, "organization_forbidden", "只能切换到本人任职的启用组织")
			return
		}
		if err != nil {
			logger.Error("switch working organization", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "切换工作组织失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": user})
	})
	authGroup.GET("/api-tokens", requireAuth, requireSession, func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.ListAPITokens(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list api tokens", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "获取 API Token 失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	authGroup.POST("/api-tokens", requireAuth, requireSession, func(c *gin.Context) {
		var input auth.CreateAPITokenInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "API Token 信息格式不正确")
			return
		}
		result, err := service.CreateAPIToken(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, auth.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_api_token", "名称不能为空且不超过 100 字，期限应为 1 至 365 天")
		case errors.Is(err, auth.ErrAPITokenLimit):
			writeAPIError(c, http.StatusConflict, "api_token_limit_reached", "每个用户最多同时保留 10 个有效 API Token")
		case errors.Is(err, auth.ErrAPITokenNameConflict):
			writeAPIError(c, http.StatusConflict, "api_token_name_exists", "已存在同名的未撤销 API Token")
		case err != nil:
			logger.Error("create api token", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "创建 API Token 失败")
		default:
			c.JSON(http.StatusCreated, result)
		}
	})
	authGroup.DELETE("/api-tokens/:id", requireAuth, requireSession, func(c *gin.Context) {
		err := service.RevokeAPIToken(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, auth.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_api_token", "API Token 标识不正确")
		case errors.Is(err, auth.ErrAPITokenNotFound):
			writeAPIError(c, http.StatusNotFound, "api_token_not_found", "API Token 不存在或已撤销")
		case err != nil:
			logger.Error("revoke api token", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "撤销 API Token 失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})
	authGroup.POST("/logout", requireAuth, requireSession, func(c *gin.Context) {
		principal := mustPrincipal(c)
		if err := service.Logout(c.Request.Context(), principal, c.GetString("request_id"), c.ClientIP()); err != nil {
			logger.Error("logout failed", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "退出登录失败")
			return
		}
		clearSessionCookie(c, cfg)
		c.Status(http.StatusNoContent)
	})
	return requireAuth
}

func authenticationMiddleware(cfg config.Config, service *auth.Service, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			principal auth.Principal
			err       error
			isBearer  bool
		)
		if authorization := strings.TrimSpace(c.GetHeader("Authorization")); authorization != "" {
			isBearer = true
			parts := strings.Fields(authorization)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeAPIError(c, http.StatusUnauthorized, "invalid_authorization", "Authorization 请求头格式不正确")
				c.Abort()
				return
			}
			principal, err = service.AuthenticateAPIToken(c.Request.Context(), parts[1])
		} else {
			var token string
			token, err = c.Cookie(cfg.SessionCookieName)
			if err == nil && token != "" {
				principal, err = service.Authenticate(c.Request.Context(), token)
			} else {
				err = auth.ErrUnauthorized
			}
		}
		if errors.Is(err, auth.ErrUnauthorized) {
			if !isBearer {
				clearSessionCookie(c, cfg)
			}
			message := "登录状态已失效，请重新登录"
			if isBearer {
				message = "API Token 无效、已过期或已撤销"
			}
			writeAPIError(c, http.StatusUnauthorized, "unauthorized", message)
			c.Abort()
			return
		}
		if err != nil {
			logger.Error("authenticate request", "error", err, "request_id", c.GetString("request_id"), "bearer", isBearer)
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "认证服务暂不可用")
			c.Abort()
			return
		}
		if principal.MustChangePassword && !allowsPasswordChangeRequest(c.Request.Method, c.Request.URL.Path) {
			writeAPIError(c, http.StatusForbidden, "password_change_required", "当前密码为临时密码，请先修改密码")
			c.Abort()
			return
		}
		c.Set(principalContextKey, principal)
		c.Next()
	}
}

func sessionAuthenticationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if mustPrincipal(c).AuthenticationType != auth.AuthenticationSession {
			writeAPIError(c, http.StatusForbidden, "browser_session_required", "该操作只能在登录后的个人账户页面完成")
			c.Abort()
			return
		}
		c.Next()
	}
}

func allowsPasswordChangeRequest(method, path string) bool {
	switch {
	case method == http.MethodGet && (path == "/api/v1/auth/me" || path == "/api/v1/auth/profile"):
		return true
	case method == http.MethodPut && path == "/api/v1/auth/profile/password":
		return true
	case method == http.MethodPost && path == "/api/v1/auth/logout":
		return true
	default:
		return false
	}
}

func mustPrincipal(c *gin.Context) auth.Principal {
	value, exists := c.Get(principalContextKey)
	if !exists {
		panic("authenticated principal is missing from Gin context")
	}
	return value.(auth.Principal)
}

func setSessionCookie(c *gin.Context, cfg config.Config, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(c *gin.Context, cfg config.Config) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
