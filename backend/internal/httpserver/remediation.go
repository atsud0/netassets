package httpserver

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"netassets/internal/pagination"
	"netassets/internal/remediation"
)

func registerRemediationRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, s *remediation.Service, logger *slog.Logger) {
	respond := func(c *gin.Context, value any, err error) {
		if errors.Is(err, remediation.ErrForbidden) || errors.Is(err, remediation.ErrNotFound) {
			if e := s.RecordDenial(c.Request.Context(), "access_denied"); e != nil {
				logger.Error("record denied remediation request", "error", e)
			}
		}
		switch {
		case errors.Is(err, remediation.ErrNotFound):
			writeAPIError(c, 404, "not_found", "任务不存在或当前无权访问")
		case errors.Is(err, remediation.ErrForbidden):
			writeAPIError(c, 403, "forbidden", "当前账号无权执行此操作；已有账号接受邀请时请先登录对应账号")
		case errors.Is(err, remediation.ErrConflict):
			writeAPIError(c, 409, "state_conflict", "状态或版本已变化，或该记录已存在，请刷新后重试")
		case errors.Is(err, remediation.ErrInvalid):
			writeAPIError(c, 400, "invalid_request", "请检查人员授权、任务期限及必填内容是否符合要求")
		case err != nil:
			logger.Error("remediation request", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, 500, "internal_error", "修复服务暂不可用")
		default:
			if value == nil {
				c.Status(204)
			} else {
				c.JSON(200, value)
			}
		}
	}
	bind := func(c *gin.Context, value any) bool {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		if err := c.ShouldBindJSON(value); err != nil {
			writeAPIError(c, 400, "invalid_request", "请求内容格式不正确")
			return false
		}
		return true
	}
	api.POST("/repair-invitations/accept", func(c *gin.Context) {
		var in remediation.AcceptInvitationInput
		if !bind(c, &in) {
			return
		}
		out, err := s.AcceptInvitation(c.Request.Context(), nil, in)
		respond(c, out, err)
	})
	secured := api.Group("", requireAuth, func(c *gin.Context) {
		p := mustPrincipal(c)
		c.Request = c.Request.WithContext(remediation.WithRequest(c.Request.Context(), remediation.RequestInfo{UserID: p.UserID, RequestID: c.GetString("request_id"), IP: c.ClientIP(), Method: c.Request.Method, Route: c.FullPath()}))
		c.Next()
	})
	secured.POST("/repair-invitations/accept-existing", func(c *gin.Context) {
		var in remediation.AcceptInvitationInput
		if !bind(c, &in) {
			return
		}
		p := mustPrincipal(c)
		out, err := s.AcceptInvitation(c.Request.Context(), &p, in)
		respond(c, out, err)
	})
	secured.GET("/vendor-catalog", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.Catalog(c.Request.Context(), mustPrincipal(c), page)
		respond(c, out, err)
	})
	saveCatalog := func(c *gin.Context) {
		var in remediation.CatalogInput
		if !bind(c, &in) {
			return
		}
		out, err := s.SaveCatalog(c.Request.Context(), mustPrincipal(c), c.Param("id"), in)
		respond(c, out, err)
	}
	secured.POST("/vendor-catalog", saveCatalog)
	secured.PATCH("/vendor-catalog/:id", saveCatalog)
	secured.GET("/vendor-memberships", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.Memberships(c.Request.Context(), mustPrincipal(c), page)
		respond(c, out, err)
	})
	secured.PATCH("/vendor-memberships/:vendor/:user", func(c *gin.Context) {
		var in struct {
			Status string    `json:"status"`
			Reason string    `json:"reason"`
			Until  time.Time `json:"valid_until"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.UpdateMembership(c.Request.Context(), mustPrincipal(c), c.Param("vendor"), c.Param("user"), in.Status, in.Reason, in.Until))
	})
	secured.GET("/remediation-settings", func(c *gin.Context) {
		out, err := s.Settings(c.Request.Context(), mustPrincipal(c))
		respond(c, out, err)
	})
	secured.PATCH("/remediation-settings", func(c *gin.Context) {
		var in struct {
			Days   int    `json:"overdue_reminder_days"`
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.SetSettings(c.Request.Context(), mustPrincipal(c), in.Days, in.Reason))
	})
	secured.GET("/remediation-options", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.Options(c.Request.Context(), mustPrincipal(c), page)
		respond(c, out, err)
	})
	secured.GET("/vendors", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.Vendors(c.Request.Context(), mustPrincipal(c), page)
		respond(c, out, err)
	})
	saveVendor := func(c *gin.Context) {
		var in struct {
			OrganizationID string `json:"organization_id"`
			Name           string `json:"name"`
			Contact        string `json:"contact"`
			Status         string `json:"status"`
		}
		if !bind(c, &in) {
			return
		}
		out, err := s.SaveVendor(c.Request.Context(), mustPrincipal(c), c.Param("id"), in.OrganizationID, in.Name, in.Contact, in.Status)
		respond(c, out, err)
	}
	secured.POST("/vendors", saveVendor)
	secured.PATCH("/vendors/:id", saveVendor)
	secured.GET("/remediation-grants", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.People(c.Request.Context(), mustPrincipal(c), c.Query("organization_id"), c.Query("business_system_id"), page)
		respond(c, out, err)
	})
	secured.POST("/remediation-grants", func(c *gin.Context) {
		var in remediation.PersonInput
		if !bind(c, &in) {
			return
		}
		out, err := s.CreatePerson(c.Request.Context(), mustPrincipal(c), in)
		respond(c, out, err)
	})
	secured.PATCH("/remediation-grants/:id", func(c *gin.Context) {
		var in struct {
			Status     string    `json:"status"`
			ValidUntil time.Time `json:"valid_until"`
			Reason     string    `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.UpdateGrant(c.Request.Context(), mustPrincipal(c), c.Param("id"), in.Status, in.ValidUntil, in.Reason))
	})
	secured.GET("/remediation-invitations", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.Invitations(c.Request.Context(), mustPrincipal(c), page)
		respond(c, out, err)
	})
	secured.POST("/remediation-invitations/:id/revoke", func(c *gin.Context) {
		var in struct {
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.RevokeInvitation(c.Request.Context(), mustPrincipal(c), c.Param("id"), in.Reason))
	})
	secured.POST("/remediation-invitations", func(c *gin.Context) {
		var in remediation.PersonInput
		if !bind(c, &in) {
			return
		}
		out, err := s.Invite(c.Request.Context(), mustPrincipal(c), in)
		respond(c, out, err)
	})
	secured.GET("/security-work-orders/:id/remediation-summary", func(c *gin.Context) {
		out, err := s.OrderSummary(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		respond(c, gin.H{"item": out}, err)
	})
	secured.GET("/security-work-orders/:id/assignable-findings", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		out, err := s.AssignableFindings(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		respond(c, out, err)
	})
	secured.POST("/security-work-orders/:id/remediation-tasks/batch-assign", func(c *gin.Context) {
		var in struct {
			Items []remediation.AssignInput `json:"items"`
		}
		if !bind(c, &in) {
			return
		}
		key := c.GetHeader("Idempotency-Key")
		if len(in.Items) < 1 || len(in.Items) > 100 || len(key) < 8 || len(key) > 100 {
			respond(c, nil, remediation.ErrInvalid)
			return
		}
		results := []remediation.Object{}
		for index, item := range in.Items {
			out, err := s.Assign(c.Request.Context(), mustPrincipal(c), c.Param("id"), fmt.Sprintf("%s:%d", key, index), item)
			if err != nil {
				code := "invalid_request"
				if errors.Is(err, remediation.ErrForbidden) || errors.Is(err, remediation.ErrNotFound) {
					code = "forbidden"
				} else if errors.Is(err, remediation.ErrConflict) {
					code = "state_conflict"
				} else if !errors.Is(err, remediation.ErrInvalid) {
					logger.Error("assign repair task", "error", err)
					code = "internal_error"
				}
				if code == "forbidden" {
					if e := s.RecordDenial(c.Request.Context(), code); e != nil {
						logger.Error("record denied assignment", "error", e)
					}
				}
				out = remediation.Object{"finding_id": item.FindingID, "error": code}
			}
			results = append(results, out)
		}
		respond(c, gin.H{"items": results}, nil)
	})
	secured.POST("/security-work-orders/:id/close-individual", func(c *gin.Context) {
		var in struct {
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.CloseOrder(c.Request.Context(), mustPrincipal(c), c.Param("id"), in.Reason))
	})
	secured.POST("/remediation-requests/:id/decide", func(c *gin.Context) {
		var in remediation.DecideInput
		if !bind(c, &in) {
			return
		}
		respond(c, nil, s.Decide(c.Request.Context(), mustPrincipal(c), c.Param("id"), in))
	})
	for _, entry := range []struct {
		path     string
		executor bool
	}{{"/repair/tasks", true}, {"/remediation-tasks", false}} {
		executor := entry.executor
		group := secured.Group(entry.path)
		group.GET("", func(c *gin.Context) {
			page, err := parsePageQuery(c)
			if err != nil {
				respond(c, nil, remediation.ErrInvalid)
				return
			}
			out, err := s.List(c.Request.Context(), mustPrincipal(c), repairFilter(c, executor, page))
			respond(c, out, err)
		})
		group.GET("/systems", func(c *gin.Context) {
			page, err := parsePageQuery(c)
			if err != nil {
				respond(c, nil, remediation.ErrInvalid)
				return
			}
			out, err := s.TaskSystems(c.Request.Context(), mustPrincipal(c), executor, page)
			respond(c, out, err)
		})
		group.GET("/statistics", func(c *gin.Context) {
			page, err := parsePageQuery(c)
			if err != nil {
				respond(c, nil, remediation.ErrInvalid)
				return
			}
			out, err := s.Statistics(c.Request.Context(), mustPrincipal(c), repairFilter(c, executor, page))
			respond(c, out, err)
		})
		group.GET("/:id", func(c *gin.Context) {
			out, err := s.Detail(c.Request.Context(), mustPrincipal(c), c.Param("id"), executor)
			respond(c, gin.H{"item": out}, err)
		})
		for _, kind := range []string{"comments", "events", "attachments", "submissions", "requests", "materials"} {
			kind := kind
			group.GET("/:id/"+kind, func(c *gin.Context) {
				page, err := parsePageQuery(c)
				if err != nil {
					respond(c, nil, remediation.ErrInvalid)
					return
				}
				out, err := s.Children(c.Request.Context(), mustPrincipal(c), c.Param("id"), kind, executor, page)
				respond(c, out, err)
			})
		}
		group.POST("/:id/actions/:action", func(c *gin.Context) {
			var in remediation.ActionInput
			if !bind(c, &in) {
				return
			}
			out, err := s.Act(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("action"), c.GetHeader("Idempotency-Key"), in, executor)
			respond(c, gin.H{"item": out}, err)
		})
		group.POST("/:id/requests", func(c *gin.Context) {
			var in remediation.RequestInput
			if !bind(c, &in) {
				return
			}
			out, err := s.Request(c.Request.Context(), mustPrincipal(c), c.Param("id"), in)
			respond(c, out, err)
		})
		group.POST("/:id/attachments", func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, remediation.MaxAttachmentSize+(1<<20))
			header, err := c.FormFile("file")
			if err != nil {
				respond(c, nil, remediation.ErrInvalid)
				return
			}
			if c.Request.MultipartForm != nil {
				defer c.Request.MultipartForm.RemoveAll()
			}
			file, err := header.Open()
			if err != nil {
				respond(c, nil, remediation.ErrInvalid)
				return
			}
			defer file.Close()
			var version int
			_, _ = fmt.Sscan(c.PostForm("expected_version"), &version)
			out, err := s.Upload(c.Request.Context(), mustPrincipal(c), c.Param("id"), remediation.AttachmentInput{Name: header.Filename, Visibility: c.PostForm("visibility"), Version: version, Reader: file}, executor)
			respond(c, out, err)
		})
		group.GET("/:id/attachments/:attachmentID/download", func(c *gin.Context) {
			out, err := s.Download(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"), executor)
			if err != nil {
				respond(c, nil, err)
				return
			}
			defer out.File.Close()
			c.Header("Content-Disposition", out.Disposition)
			c.Header("X-Content-Type-Options", "nosniff")
			c.Header("Cache-Control", "private, no-store")
			c.Header("Content-Length", fmt.Sprint(out.Size))
			c.Header("Content-Type", "application/octet-stream")
			c.Status(200)
			_, err = io.Copy(c.Writer, out.File)
			if auditErr := s.RecordDownloadResult(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"), err == nil); auditErr != nil {
				logger.Error("record repair download result", "error", auditErr)
			}
			if err != nil {
				logger.Warn("repair download interrupted", "error", err)
			}
		})
	}
}

func repairFilter(c *gin.Context, executor bool, page pagination.Query) remediation.Filter {
	return remediation.Filter{OrderID: c.Query("work_order_id"), Status: c.Query("status"), Keyword: c.Query("keyword"), Mine: executor, Overdue: c.Query("overdue") == "true", DueSoon: c.Query("due_soon") == "true", Returned: c.Query("returned") == "true", NeedsReassignment: c.Query("needs_reassignment") == "true", MaterialNeedsReview: c.Query("material_needs_review") == "true", Blocked: c.Query("blocked") == "true", PendingRequest: c.Query("pending_request") == "true", ReviewMine: c.Query("review_mine") == "true", SystemID: c.Query("business_system_id"), Severity: c.Query("severity"), ExecutionBlockedReason: c.Query("execution_blocked_reason"), Revived: c.Query("revived") == "true", Page: page}
}
