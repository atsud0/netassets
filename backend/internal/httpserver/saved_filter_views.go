package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/savedfilter"
)

func registerSavedFilterRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *savedfilter.Service, logger *slog.Logger) {
	group := api.Group("/saved-filter-views", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), c.Query("page_key"), page)
		switch {
		case errors.Is(err, savedfilter.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_page", "保存视图页面标识不正确")
		case err != nil:
			logger.Error("list saved filter views", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取保存视图失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.POST("", func(c *gin.Context) {
		var input savedfilter.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "保存视图信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeSavedFilterResult(c, logger, "create saved filter view", http.StatusCreated, item, err)
	})
	group.PATCH("/:id", func(c *gin.Context) {
		var input savedfilter.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "保存视图信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSavedFilterResult(c, logger, "update saved filter view", http.StatusOK, item, err)
	})
	group.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, savedfilter.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_view", "保存视图标识不正确")
		case errors.Is(err, savedfilter.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "view_not_found", "保存视图不存在或不属于当前用户")
		case err != nil:
			logger.Error("delete saved filter view", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "删除保存视图失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})
}

func writeSavedFilterResult(c *gin.Context, logger *slog.Logger, action string, status int, item savedfilter.View, err error) {
	switch {
	case errors.Is(err, savedfilter.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_view", "请检查页面、名称和筛选条件；名称最多 100 字")
	case errors.Is(err, savedfilter.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "view_not_found", "保存视图不存在或不属于当前用户")
	case errors.Is(err, savedfilter.ErrConflict):
		writeAPIError(c, http.StatusConflict, "view_name_exists", "当前页面已存在同名保存视图")
	case errors.Is(err, savedfilter.ErrLimit):
		writeAPIError(c, http.StatusConflict, "view_limit_reached", "每个页面最多保存 20 个个人视图")
	case err != nil:
		logger.Error(action, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "保存视图操作失败")
	default:
		c.JSON(status, gin.H{"item": item})
	}
}
