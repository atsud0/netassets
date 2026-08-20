package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/assetcandidate"
	"netassets/internal/assetobservation"
	"netassets/internal/auditlog"
	"netassets/internal/auth"
	"netassets/internal/backup"
	"netassets/internal/buildinfo"
	"netassets/internal/businesssystem"
	"netassets/internal/changehistory"
	"netassets/internal/config"
	"netassets/internal/customfield"
	"netassets/internal/dashboard"
	"netassets/internal/dataquality"
	"netassets/internal/exportjob"
	"netassets/internal/inventorytask"
	"netassets/internal/networkarea"
	"netassets/internal/notification"
	"netassets/internal/organization"
	"netassets/internal/recyclebin"
	roleDomain "netassets/internal/role"
	"netassets/internal/savedfilter"
	"netassets/internal/securityreference"
	"netassets/internal/securitytestbatch"
	"netassets/internal/securityworkorder"
	userdomain "netassets/internal/user"
)

func New(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	return NewWithContext(context.Background(), cfg, pool, logger)
}

func NewWithContext(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery(), requestLogger(logger), securityHeaders(), requestID())

	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", readinessHandler(pool))

	api := router.Group("/api/v1")
	api.GET("/system/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name":        "NetAssets",
			"version":     buildinfo.Version,
			"commit":      buildinfo.Commit,
			"environment": cfg.Environment,
		})
	})
	if pool != nil {
		backupService := backup.NewService(pool, cfg.DatabaseURL, cfg.DataDir)
		router.Use(writeMaintenanceMiddleware(backupService.WriteGate()))
		authService := auth.NewService(pool, cfg.SessionTTL)
		requireAuth := registerAuthRoutes(api, cfg, authService, logger)
		registerOrganizationRoutes(api, requireAuth, organization.NewService(pool), logger)
		registerUserRoutes(api, requireAuth, userdomain.NewService(pool), logger)
		registerRoleRoutes(api, requireAuth, roleDomain.NewService(pool), logger)
		customFieldService := customfield.NewService(pool)
		businessSystemService := businesssystem.NewService(pool)
		registerBusinessSystemRoutes(api, requireAuth, businessSystemService, customFieldService, logger)
		assetService := asset.NewService(pool)
		registerAssetRoutes(api, requireAuth, assetService, customFieldService, logger)
		registerAssetObservationRoutes(api, requireAuth, assetobservation.NewService(pool), logger)
		registerAssetCandidateRoutes(api, requireAuth, assetcandidate.NewService(pool, assetService), logger)
		registerNetworkAreaRoutes(api, requireAuth, networkarea.NewService(pool), logger)
		registerCustomFieldRoutes(api, requireAuth, customFieldService, logger)
		dataQualityService := dataquality.NewService(pool)
		registerDataQualityRuleRoutes(api, requireAuth, dataQualityService, logger)
		autoScanSchedule, err := dataquality.NewAutoScanSchedule(
			cfg.DataQualityAutoScanEnabled,
			cfg.DataQualityAutoScanHour,
			cfg.DataQualityAutoScanMinute,
			cfg.DataQualityAutoScanTimezone,
		)
		if err != nil {
			logger.Error("configure automatic data quality scan", "error", err)
		} else {
			dataQualityService.StartAutoScan(ctx, autoScanSchedule, backupService.WriteGate(), logger)
		}
		inventoryTaskService := inventorytask.NewService(pool)
		registerInventoryTaskRoutes(api, requireAuth, inventoryTaskService, logger)
		exportJobService := exportjob.NewService(pool, cfg.DataDir, assetService, businessSystemService, inventoryTaskService, logger)
		registerExportJobRoutes(api, requireAuth, exportJobService, logger)
		exportJobService.Start(ctx)
		registerDashboardRoutes(api, requireAuth, dashboard.NewService(pool), logger)
		registerAuditLogRoutes(api, requireAuth, auditlog.NewService(pool), logger)
		registerChangeHistoryRoutes(api, requireAuth, changehistory.NewService(pool), logger)
		registerRecycleBinRoutes(api, requireAuth, recyclebin.NewService(pool), logger)
		registerSavedFilterRoutes(api, requireAuth, savedfilter.NewService(pool), logger)
		registerNotificationRoutes(api, requireAuth, notification.NewService(pool), logger)
		registerSecurityReferenceRoutes(api, requireAuth, securityreference.NewService(pool), logger)
		registerSecurityWorkOrderRoutes(api, requireAuth, securityworkorder.NewService(pool, cfg.DataDir), logger)
		registerSecurityTestBatchRoutes(api, requireAuth, securitytestbatch.NewService(pool, cfg.DataDir), logger)
		registerBackupRoutes(api, requireAuth, backupService, logger)
	}

	registerStaticFrontend(router, cfg.StaticDir)
	return router
}

func readinessHandler(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pool == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "database is not configured"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "database is unreachable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", c.GetString("request_id"),
			"client_ip", c.ClientIP(),
		)
	}
}

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = time.Now().UTC().Format("20060102T150405.000000000")
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	}
}

func registerStaticFrontend(router *gin.Engine, staticDir string) {
	info, err := os.Stat(staticDir)
	if err != nil || !info.IsDir() {
		return
	}
	assetsDir := filepath.Join(staticDir, "_assets")
	if _, err := os.Stat(assetsDir); err == nil {
		router.Static("/_assets", assetsDir)
	}
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/health/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.File(filepath.Join(staticDir, "index.html"))
	})
}
