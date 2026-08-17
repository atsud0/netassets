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
	"netassets/internal/customfield"
	"netassets/internal/exportfile"
)

const maxAssetImportBytes = 10 << 20

func splitSelectedAssetIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		ids = append(ids, strings.TrimSpace(part))
	}
	return ids
}

func registerAssetRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *asset.Service, customFields *customfield.Service, logger *slog.Logger) {
	group := api.Group("/assets", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchWithFilter(c.Request.Context(), mustPrincipal(c), asset.SearchFilter{
			OrganizationID: strings.TrimSpace(c.Query("organization_id")),
			Type:           strings.TrimSpace(c.Query("type")),
			Status:         strings.TrimSpace(c.Query("status")),
			Keyword:        strings.TrimSpace(c.Query("q")),
			Page:           page,
		})
		if errors.Is(err, asset.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看网络资产")
			return
		}
		if err != nil {
			logger.Error("list assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取网络资产失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})

	group.GET("/export", func(c *gin.Context) {
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "xlsx"))), ".")
		if format != "csv" && format != "xlsx" {
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "导出格式仅支持 csv 或 xlsx")
			return
		}
		selectedIDs := splitSelectedAssetIDs(c.Query("selected_ids"))
		filter := asset.ExportFilter{
			OrganizationID: strings.TrimSpace(c.Query("organization_id")),
			Type:           strings.TrimSpace(c.Query("type")),
			Status:         strings.TrimSpace(c.Query("status")),
			Keyword:        strings.TrimSpace(c.Query("q")),
			SelectedIDs:    selectedIDs,
		}
		extendExportWriteDeadline(c, logger)
		if isCSVExport(format) {
			_, err := service.StreamCSVExport(c.Request.Context(), mustPrincipal(c), filter, c.Writer, func(meta exportfile.Metadata) {
				if len(selectedIDs) > 0 {
					meta.FileName = "网络资产选中项.csv"
					setStreamingExportHeaders(c, "network-assets-selected.csv", meta)
					return
				}
				setStreamingExportHeaders(c, "network-assets-ledger.csv", meta)
			}, c.GetString("request_id"), c.ClientIP())
			if err == nil {
				return
			}
			if c.Writer.Written() {
				logger.Error("stream assets csv", "error", err, "written_bytes", c.Writer.Size(), "request_id", c.GetString("request_id"))
				c.Abort()
				return
			}
			clearStreamingExportHeaders(c)
			if errors.Is(err, asset.ErrForbidden) {
				writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权导出网络资产")
				return
			}
			if errors.Is(err, asset.ErrInvalid) {
				writeAPIError(c, http.StatusBadRequest, "invalid_selection", "选中资产参数不正确，单次最多导出 200 条")
				return
			}
			logger.Error("export assets csv", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出网络资产台账失败")
			return
		}

		data, contentType, fileName, count, err := service.Export(c.Request.Context(), mustPrincipal(c), filter, format, c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, asset.ErrExportTooLarge) {
			writeAPIError(c, http.StatusUnprocessableEntity, "synchronous_xlsx_limit_exceeded", "同步 XLSX 导出最多支持 5000 条数据，请改用 CSV 格式导出")
			return
		}
		if errors.Is(err, asset.ErrInvalid) {
			if len(selectedIDs) > 0 {
				writeAPIError(c, http.StatusBadRequest, "invalid_selection", "选中资产参数不正确，单次最多导出 200 条")
			} else {
				writeAPIError(c, http.StatusBadRequest, "invalid_format", "导出格式仅支持 csv 或 xlsx")
			}
			return
		}
		if err != nil {
			logger.Error("export assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出网络资产台账失败")
			return
		}
		downloadBaseName := "network-assets-ledger"
		if len(selectedIDs) > 0 {
			downloadBaseName = "network-assets-selected"
			fileName = "网络资产选中项." + strings.TrimPrefix(strings.ToLower(format), ".")
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s; filename*=UTF-8''%s", downloadBaseName, strings.TrimPrefix(strings.ToLower(format), "."), url.PathEscape(fileName)))
		c.Header("X-Export-Count", fmt.Sprintf("%d", count))
		c.Data(http.StatusOK, contentType, data)
	})

	group.GET("/imports", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchImports(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list asset imports", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取资产导入记录失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})

	group.PATCH("/batch", func(c *gin.Context) {
		var input asset.BatchUpdateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "批量更新信息格式不正确")
			return
		}
		result, err := service.UpdateBatch(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_batch", "单次请选择 1 至 200 个资产，并指定有效状态或网络区域")
		case err != nil:
			logger.Error("batch update assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量更新网络资产失败")
		default:
			c.JSON(http.StatusOK, gin.H{"result": result})
		}
	})

	group.GET("/owner-options", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchOwnerOptions(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("organization_id")), strings.TrimSpace(c.Query("q")), page)
		switch {
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_organization", "请选择责任人所属组织")
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该组织的责任人候选")
		case err != nil:
			logger.Error("list asset owner options", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取责任人候选失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})

	group.GET("/:id/custom-fields", func(c *gin.Context) {
		items, err := customFields.ActiveDefinitions(c.Request.Context(), mustPrincipal(c), "asset", c.Param("id"))
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		values, err := customFields.Values(c.Request.Context(), mustPrincipal(c), "asset", c.Param("id"))
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"definitions": items, "values": values})
	})
	group.PUT("/:id/custom-fields", func(c *gin.Context) {
		var input customfield.ValuesInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查自定义字段值")
			return
		}
		if err := customFields.SaveValues(c.Request.Context(), mustPrincipal(c), "asset", c.Param("id"), input, c.GetString("request_id"), c.ClientIP()); err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.Status(http.StatusNoContent)
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
		organizationID := strings.TrimSpace(c.Query("organization_id"))
		if organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请先选择导入所属组织，再下载模板")
			return
		}
		data, contentType, fileName, err := service.Template(c.Request.Context(), mustPrincipal(c), organizationID, c.DefaultQuery("format", "xlsx"))
		switch {
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "模板格式仅支持 csv 或 xlsx")
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权为该组织下载资产导入模板")
		case err != nil:
			logger.Error("build asset import template", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "生成导入模板失败")
		default:
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=asset-import-template.%s; filename*=UTF-8''%s", strings.TrimPrefix(strings.ToLower(c.DefaultQuery("format", "xlsx")), "."), url.PathEscape(fileName)))
			c.Data(http.StatusOK, contentType, data)
		}
	})

	group.POST("/import/columns", func(c *gin.Context) {
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
		analysis, err := service.AnalyzeImportColumns(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file)
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权分析该组织的网络资产导入文件")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", "文件格式、列数或数据行数不符合要求（最多 200 列、5000 行）")
		case err != nil:
			logger.Error("analyze asset import columns", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "分析网络资产导入文件失败")
		default:
			c.JSON(http.StatusOK, analysis)
		}
	})
	group.POST("/import/preview", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAssetImportBytes)
		organizationID := strings.TrimSpace(c.PostForm("organization_id"))
		importMode := strings.TrimSpace(c.PostForm("import_mode"))
		columnMapping, mappingErr := parseImportColumnMapping(c.PostForm("column_mapping"))
		if mappingErr != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_column_mapping", "字段映射格式不正确")
			return
		}
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
		result, err := service.PreviewImport(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file, importMode, asset.ColumnMapping(columnMapping))
		switch {
		case errors.Is(err, asset.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权预检该组织的资产导入文件")
		case errors.Is(err, asset.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", "文件格式、表头或数据行数不符合要求（最多 5000 行）")
		case err != nil:
			logger.Error("preview 网络资产 import", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "预检网络资产导入文件失败")
		default:
			c.JSON(http.StatusOK, gin.H{"result": result})
		}
	})
	group.POST("/import", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAssetImportBytes)
		organizationID := strings.TrimSpace(c.PostForm("organization_id"))
		importMode := strings.TrimSpace(c.PostForm("import_mode"))
		columnMapping, mappingErr := parseImportColumnMapping(c.PostForm("column_mapping"))
		if mappingErr != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_column_mapping", "字段映射格式不正确")
			return
		}
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

		result, err := service.Import(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file, importMode, c.GetString("request_id"), c.ClientIP(), asset.ColumnMapping(columnMapping))
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
