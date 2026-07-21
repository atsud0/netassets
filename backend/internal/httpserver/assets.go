package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/asset"
)

const maxAssetImportBytes = 10 << 20

func registerAssetRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *asset.Service, logger *slog.Logger) {
	group := api.Group("/assets", requireAuth)
	group.GET("", func(c *gin.Context) {
		items, err := service.List(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("list assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取网络资产失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
	})

	group.GET("/imports", func(c *gin.Context) {
		items, err := service.ListImports(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("list asset imports", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取资产导入记录失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
	})

	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该资产")
		case errors.Is(err, asset.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "asset_not_found", "网络资产不存在")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_asset_id", "资产编号不正确")
		case err != nil:
			logger.Error("get asset", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取网络资产失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})

	group.POST("", func(c *gin.Context) {
		var input asset.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "网络资产信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, "manual", c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权在该组织新增资产")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_asset", "请检查资产类型、资产值和归属信息")
		case errors.Is(err, asset.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "reference_not_found", "所属组织或业务系统不存在")
		case errors.Is(err, asset.ErrConflict):
			writeAPIError(c, http.StatusConflict, "asset_exists", "相同类型和资产值已存在")
		case err != nil:
			logger.Error("create asset", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增网络资产失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})

	group.PATCH("/:id", func(c *gin.Context) {
		var input asset.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "网络资产信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权编辑该资产或变更其所属组织")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_asset", "请检查资产类型、资产值、状态和归属信息")
		case errors.Is(err, asset.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "reference_not_found", "网络资产、所属组织或业务系统不存在")
		case errors.Is(err, asset.ErrConflict):
			writeAPIError(c, http.StatusConflict, "asset_exists", "相同类型和资产值已存在")
		case err != nil:
			logger.Error("update asset", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "编辑网络资产失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})

	group.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权删除该资产")
		case errors.Is(err, asset.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "asset_not_found", "网络资产不存在")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_asset_id", "资产编号不正确")
		case err != nil:
			logger.Error("delete asset", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "删除网络资产失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})

	group.GET("/import-template", func(c *gin.Context) {
		data, contentType, fileName, err := asset.Template(c.DefaultQuery("format", "xlsx"))
		if errors.Is(err, asset.ErrInvalid) {
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "模板格式仅支持 csv 或 xlsx")
			return
		}
		if err != nil {
			logger.Error("build asset import template", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "生成导入模板失败")
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=asset-import-template.%s; filename*=UTF-8''%s", strings.TrimPrefix(strings.ToLower(c.DefaultQuery("format", "xlsx")), "."), url.PathEscape(fileName)))
		c.Data(http.StatusOK, contentType, data)
	})

	group.POST("/import", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAssetImportBytes)
		organizationID := strings.TrimSpace(c.PostForm("organization_id"))
		fileHeader, err := c.FormFile("file")
		if err != nil || organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择所属组织并上传 CSV 或 XLSX 文件")
			return
		}
		if fileHeader.Size > maxAssetImportBytes {
			writeAPIError(c, http.StatusRequestEntityTooLarge, "file_too_large", "导入文件不能超过 10 MB")
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_file", "无法读取导入文件")
			return
		}
		defer file.Close()

		result, err := service.Import(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权向该组织导入资产")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", "文件格式、表头或数据行数不符合要求（最多 5000 行）")
		case err != nil:
			logger.Error("import assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量导入网络资产失败")
		default:
			c.JSON(http.StatusOK, gin.H{"result": result})
		}
	})
}
