package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/businesssystem"
	"netassets/internal/customfield"
	"netassets/internal/exportfile"
)

func splitSelectedBusinessSystemIDs(raw string) []string {
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

func registerBusinessSystemRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *businesssystem.Service, customFields *customfield.Service, logger *slog.Logger) {
	group := api.Group("/business-systems", requireAuth)
	group.GET("/imports", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchImports(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list business system imports", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统导入记录失败")
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
		selectedIDs := splitSelectedBusinessSystemIDs(c.Query("selected_ids"))
		filter := businesssystem.SearchFilter{
			OrganizationID: strings.TrimSpace(c.Query("organization_id")),
			Status:         strings.TrimSpace(c.Query("status")),
			Keyword:        strings.TrimSpace(c.Query("q")),
			SelectedIDs:    selectedIDs,
		}
		extendExportWriteDeadline(c, logger)
		if isCSVExport(format) {
			_, err := service.StreamCSVExport(c.Request.Context(), mustPrincipal(c), filter, c.Writer, func(meta exportfile.Metadata) {
				if len(selectedIDs) > 0 {
					meta.FileName = "业务系统选中项.csv"
					setStreamingExportHeaders(c, "business-systems-selected.csv", meta)
					return
				}
				setStreamingExportHeaders(c, "business-systems-ledger.csv", meta)
			}, c.GetString("request_id"), c.ClientIP())
			if err == nil {
				return
			}
			if c.Writer.Written() {
				logger.Error("stream business systems csv", "error", err, "written_bytes", c.Writer.Size(), "request_id", c.GetString("request_id"))
				c.Abort()
				return
			}
			clearStreamingExportHeaders(c)
			if errors.Is(err, businesssystem.ErrForbidden) {
				writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权导出业务系统")
				return
			}
			if errors.Is(err, businesssystem.ErrInvalid) {
				writeAPIError(c, http.StatusBadRequest, "invalid_selection", businessSystemInputErrorMessage(err, "选中业务系统参数不正确，单次最多导出 200 条"))
				return
			}
			logger.Error("export business systems csv", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出业务系统台账失败")
			return
		}

		data, contentType, fileName, count, err := service.Export(c.Request.Context(), mustPrincipal(c), filter, format, c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, businesssystem.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权导出业务系统")
			return
		}
		if errors.Is(err, businesssystem.ErrExportTooLarge) {
			writeAPIError(c, http.StatusUnprocessableEntity, "synchronous_xlsx_limit_exceeded", "同步 XLSX 导出最多支持 5000 条数据，请改用 CSV 格式导出")
			return
		}
		if errors.Is(err, businesssystem.ErrInvalid) {
			if len(selectedIDs) > 0 {
				writeAPIError(c, http.StatusBadRequest, "invalid_selection", businessSystemInputErrorMessage(err, "选中业务系统参数不正确，单次最多导出 200 条"))
			} else {
				writeAPIError(c, http.StatusBadRequest, "invalid_format", "导出格式仅支持 csv 或 xlsx")
			}
			return
		}
		if err != nil {
			logger.Error("export business systems", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出业务系统台账失败")
			return
		}
		downloadBaseName := "business-systems-ledger"
		if len(selectedIDs) > 0 {
			downloadBaseName = "business-systems-selected"
			fileName = "业务系统选中项." + format
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s; filename*=UTF-8''%s", downloadBaseName, format, url.PathEscape(fileName)))
		c.Header("X-Export-Count", fmt.Sprintf("%d", count))
		c.Data(http.StatusOK, contentType, data)
	})

	group.GET("/import-template", func(c *gin.Context) {
		organizationID := strings.TrimSpace(c.Query("organization_id"))
		if organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请先选择导入所属组织，再下载模板")
			return
		}
		format := c.DefaultQuery("format", "xlsx")
		data, contentType, fileName, err := service.Template(c.Request.Context(), mustPrincipal(c), organizationID, format)
		switch {
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "模板格式仅支持 csv 或 xlsx")
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权为该组织下载业务系统导入模板")
		case err != nil:
			logger.Error("build business system import template", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "生成导入模板失败")
		default:
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=business-system-import-template.%s; filename*=UTF-8''%s", strings.TrimPrefix(strings.ToLower(format), "."), url.PathEscape(fileName)))
			c.Data(http.StatusOK, contentType, data)
		}
	})
	group.POST("/import/columns", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAssetImportBytes)
		organizationID := strings.TrimSpace(c.PostForm("organization_id"))
		fileHeader, err := c.FormFile("file")
		if organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "所属组织：请选择导入数据所属组织")
			return
		}
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "导入文件：请上传 CSV 或 XLSX 文件")
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
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权分析该组织的业务系统导入文件")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", businessSystemInputErrorMessage(err, "文件格式、列数或数据行数不符合要求（最多 5000 行；列数上限为 200 与当前配置字段数的较大值）"))
		case err != nil:
			logger.Error("analyze business system import columns", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "分析业务系统导入文件失败")
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
		if organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "所属组织：请选择导入数据所属组织")
			return
		}
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "导入文件：请上传 CSV 或 XLSX 文件")
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
		result, err := service.PreviewImport(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file, importMode, businesssystem.ColumnMapping(columnMapping))
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权预检该组织的业务系统导入文件")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", businessSystemInputErrorMessage(err, "文件格式、表头或数据行数不符合要求（最多 5000 行）"))
		case err != nil:
			logger.Error("preview 业务系统 import", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "预检业务系统导入文件失败")
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
		if organizationID == "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "所属组织：请选择导入数据所属组织")
			return
		}
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "导入文件：请上传 CSV 或 XLSX 文件")
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
		result, err := service.Import(c.Request.Context(), mustPrincipal(c), organizationID, fileHeader.Filename, file, importMode, c.GetString("request_id"), c.ClientIP(), businesssystem.ColumnMapping(columnMapping))
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权向该组织导入业务系统")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_import", businessSystemInputErrorMessage(err, "文件格式、表头或数据行数不符合要求（最多 5000 行）"))
		case err != nil:
			logger.Error("import business systems", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量导入业务系统失败")
		default:
			c.JSON(http.StatusOK, gin.H{"result": result})
		}
	})
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchWithFilter(c.Request.Context(), mustPrincipal(c), businesssystem.SearchFilter{
			OrganizationID: strings.TrimSpace(c.Query("organization_id")),
			Status:         strings.TrimSpace(c.Query("status")),
			Keyword:        strings.TrimSpace(c.Query("q")),
			Page:           page,
		})
		if errors.Is(err, businesssystem.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看业务系统")
			return
		}
		if err != nil {
			logger.Error("list business systems", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("/batch/archive", func(c *gin.Context) {
		var input businesssystem.BatchArchiveInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择要批量处理的业务系统")
			return
		}
		result, err := service.SetArchivedBatch(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权批量处理业务系统")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_batch", businessSystemInputErrorMessage(err, "请选择带有最新版本号的业务系统"))
		case err != nil:
			logger.Error("batch change business system status", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量处理业务系统失败")
		default:
			c.JSON(http.StatusOK, gin.H{"result": result})
		}
	})

	group.GET("/:id/custom-fields", func(c *gin.Context) {
		items, err := customFields.ActiveDefinitions(c.Request.Context(), mustPrincipal(c), "business_system", c.Param("id"))
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		values, err := customFields.Values(c.Request.Context(), mustPrincipal(c), "business_system", c.Param("id"))
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
		if err := customFields.SaveValues(c.Request.Context(), mustPrincipal(c), "business_system", c.Param("id"), input, c.GetString("request_id"), c.ClientIP()); err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.GET("/:id/assets", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchRelatedAssets(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该业务系统的关联资产")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system_id", "业务系统编号不正确")
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统不存在")
		case err != nil:
			logger.Error("list related business system assets", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统关联资产失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})

	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该业务系统")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system_id", "业务系统编号不正确")
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统不存在")
		case err != nil:
			logger.Error("get business system", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统详情失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})

	group.POST("", func(c *gin.Context) {
		var input businesssystem.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权在该组织新增业务系统")
		case errors.Is(err, businesssystem.ErrInvalidOwnerContact):
			writeAPIError(c, http.StatusBadRequest, "invalid_owner_contact", "联系方式格式不正确，请填写手机号码、座机或分机号")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system", businessSystemInputErrorMessage(err, "请检查系统信息和数据安全属性"))
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "所属组织不存在")
		case errors.Is(err, businesssystem.ErrConflict):
			writeAPIError(c, http.StatusConflict, "system_code_exists", "系统编码已存在")
		case err != nil:
			logger.Error("create business system", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增业务系统失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})

	group.PATCH("/:id", func(c *gin.Context) {
		var input businesssystem.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, businesssystem.ErrVersionRequired):
			writeAPIError(c, http.StatusBadRequest, "expected_version_required", "编辑业务系统需要提供 expected_version")
		case errors.Is(err, businesssystem.ErrEditConflict):
			writeAPIError(c, http.StatusConflict, "working_version_conflict", "该业务系统工作版本已被其他人更新，请刷新后重试")
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权编辑该业务系统或变更其所属组织")
		case errors.Is(err, businesssystem.ErrInvalidOwnerContact):
			writeAPIError(c, http.StatusBadRequest, "invalid_owner_contact", "联系方式格式不正确，请填写手机号码、座机或分机号")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system", businessSystemInputErrorMessage(err, "请检查系统信息和数据安全属性"))
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统或所属组织不存在")
		case errors.Is(err, businesssystem.ErrFormalVersionConflict):
			writeAPIError(c, http.StatusConflict, "formal_version_conflict", "业务系统已被其他人修改，请刷新后重试")
		case errors.Is(err, businesssystem.ErrConflict):
			writeAPIError(c, http.StatusConflict, "system_code_exists", "系统编码已存在")
		case errors.Is(err, businesssystem.ErrInUse):
			writeAPIError(c, http.StatusConflict, "system_has_assets", "该系统已关联网络资产，不能变更所属组织")
		case err != nil:
			logger.Error("update business system", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "编辑业务系统失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})

	group.POST("/:id/archive", func(c *gin.Context) {
		item, err := service.SetArchived(c.Request.Context(), mustPrincipal(c), c.Param("id"), true, parseBusinessSystemExpectedVersion(c), c.GetString("request_id"), c.ClientIP())
		handleBusinessSystemStatusResult(c, item, err, logger, "归档")
	})
	group.POST("/:id/restore", func(c *gin.Context) {
		item, err := service.SetArchived(c.Request.Context(), mustPrincipal(c), c.Param("id"), false, parseBusinessSystemExpectedVersion(c), c.GetString("request_id"), c.ClientIP())
		handleBusinessSystemStatusResult(c, item, err, logger, "恢复")
	})

	group.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), parseBusinessSystemExpectedVersion(c), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, businesssystem.ErrVersionRequired):
			writeAPIError(c, http.StatusBadRequest, "expected_version_required", "删除业务系统需要提供 expected_version")
		case errors.Is(err, businesssystem.ErrEditConflict):
			writeAPIError(c, http.StatusConflict, "working_version_conflict", "该业务系统工作版本已被其他人更新，请刷新后重试")
		case errors.Is(err, businesssystem.ErrFormalVersionConflict):
			writeAPIError(c, http.StatusConflict, "formal_version_conflict", "业务系统已被其他人修改，请刷新后重试")
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权删除该业务系统")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system_id", businessSystemInputErrorMessage(err, "业务系统编号不正确"))
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统不存在")
		case errors.Is(err, businesssystem.ErrInUse):
			writeAPIError(c, http.StatusConflict, "system_has_assets", "该系统仍关联网络资产，请先解除关联；如需停用可选择归档")
		case err != nil:
			logger.Error("delete business system", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "删除业务系统失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})

}

func handleBusinessSystemStatusResult(c *gin.Context, item businesssystem.BusinessSystem, err error, logger *slog.Logger, action string) {
	switch {
	case errors.Is(err, businesssystem.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权"+action+"该业务系统")
	case errors.Is(err, businesssystem.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_business_system_id", "业务系统编号不正确")
	case errors.Is(err, businesssystem.ErrVersionRequired):
		writeAPIError(c, http.StatusBadRequest, "expected_version_required", "归档或恢复业务系统需要提供 expected_version")
	case errors.Is(err, businesssystem.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统不存在")
	case errors.Is(err, businesssystem.ErrFormalVersionConflict):
		writeAPIError(c, http.StatusConflict, "formal_version_conflict", "业务系统已被其他人修改，请刷新后重试")
	case err != nil:
		logger.Error("change business system status", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", action+"业务系统失败")
	default:
		c.JSON(http.StatusOK, gin.H{"item": item})
	}
}

// parseBusinessSystemExpectedVersion reads the optimistic-lock value for direct
// working-space writes. A missing value is not silently treated as 0: the
// service layer must reject it so clients cannot drop the CAS check.
func parseBusinessSystemExpectedVersion(c *gin.Context) *int {
	raw := strings.TrimSpace(c.Query("expected_version"))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}
