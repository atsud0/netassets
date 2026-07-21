package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
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
	authGroup.GET("/me", requireAuth, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": mustPrincipal(c)})
	})
	authGroup.POST("/logout", requireAuth, func(c *gin.Context) {
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
		token, err := c.Cookie(cfg.SessionCookieName)
		if err != nil || token == "" {
			clearSessionCookie(c, cfg)
			writeAPIError(c, http.StatusUnauthorized, "unauthorized", "请先登录")
			c.Abort()
			return
		}
		principal, err := service.Authenticate(c.Request.Context(), token)
		if errors.Is(err, auth.ErrUnauthorized) {
			clearSessionCookie(c, cfg)
			writeAPIError(c, http.StatusUnauthorized, "unauthorized", "登录状态已失效，请重新登录")
			c.Abort()
			return
		}
		if err != nil {
			logger.Error("authenticate request", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "认证服务暂不可用")
			c.Abort()
			return
		}
		c.Set(principalContextKey, principal)
		c.Next()
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
