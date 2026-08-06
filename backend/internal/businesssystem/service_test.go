package businesssystem

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"netassets/internal/auth"
	"netassets/internal/exportfile"
)

func validBusinessSystemInput() CreateInput {
	return CreateInput{
		Code:                                 "CRM_001",
		Name:                                 "客户关系管理系统",
		OrganizationID:                       "00000000-0000-0000-0000-000000000001",
		Status:                               "running",
		Level:                                "normal",
		InternetExposure:                     "unknown",
		ContainsPersonalInformation:          "unknown",
		ContainsSensitivePersonalInformation: "unknown",
		DataVolumeOver100K:                   "unknown",
		ContainsImportantData:                "unknown",
		ContainsCoreData:                     "unknown",
	}
}

func TestImportsUseWorkingInventoryOutsideGroupAdmin(t *testing.T) {
	if !usesWorkingInventory(auth.Principal{Roles: []string{"organization_admin"}}) {
		t.Fatal("organization administrator import must use the organization working inventory")
	}
	if !usesWorkingInventory(auth.Principal{Roles: []string{"organization_editor"}}) {
		t.Fatal("organization editor import must use the organization working inventory")
	}
	if usesWorkingInventory(auth.Principal{Roles: []string{"group_admin"}}) {
		t.Fatal("group administrator import must continue writing the formal ledger")
	}
}

func TestNormalizeBusinessSystemInput(t *testing.T) {
	input := validBusinessSystemInput()
	input.Code = "  CRM_001  "
	input.Name = "  客户关系管理系统  "
	input.OwnerContact = "  13800000000  "

	normalize(&input)

	if input.Code != "CRM_001" || input.Name != "客户关系管理系统" || input.OwnerContact != "13800000000" {
		t.Fatalf("normalize result = %#v", input)
	}
}

func TestValidBusinessSystemInputRejectsSensitiveInformationWithoutPersonalInformation(t *testing.T) {
	input := validBusinessSystemInput()
	input.ContainsPersonalInformation = "no"
	input.ContainsSensitivePersonalInformation = "yes"

	if validInput(input) {
		t.Fatal("validInput accepted sensitive personal information when personal information is no")
	}
}

func TestValidBusinessSystemInputAcceptsPendingDataClassification(t *testing.T) {
	input := validBusinessSystemInput()
	input.ContainsImportantData = "pending"
	input.ContainsCoreData = "pending"

	if !validInput(input) {
		t.Fatal("validInput rejected pending data classification")
	}
}

func TestMapHeadersRecognizesChineseAndEnglishAliases(t *testing.T) {
	headers := mapHeaders([]string{"\ufeff系统编码", "system_name", "是否包含个人信息", "data_volume_over_100k"})
	if headers["code"] != 0 || headers["name"] != 1 || headers["contains_personal_information"] != 2 || headers["data_volume_over_100k"] != 3 {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestImportInputTranslatesEnumsAndCalculatesRecordCount(t *testing.T) {
	headers := mapHeaders([]string{"系统编码", "系统名称", "运行状态", "系统等级", "互联网暴露情况", "是否包含个人信息", "是否包含敏感个人信息", "个人信息记录数", "是否包含重要数据", "是否包含核心数据"})
	input, rowErr := importInput([]string{"ERP_001", "企业资源系统", "运行中", "重要", "部分", "是", "否", "120000", "待认定", "待确认"}, headers, "00000000-0000-0000-0000-000000000001")
	if rowErr != nil {
		t.Fatalf("importInput returned error: %#v", rowErr)
	}
	if input.Status != "running" || input.Level != "important" || input.InternetExposure != "partial" {
		t.Fatalf("unexpected enum translation: %#v", input)
	}
	if input.PersonalInformationRecordCount == nil || *input.PersonalInformationRecordCount != 120000 {
		t.Fatalf("unexpected record count: %#v", input.PersonalInformationRecordCount)
	}
}

func TestImportInputRejectsMissingRequiredFields(t *testing.T) {
	headers := mapHeaders([]string{"系统编码", "系统名称"})
	_, rowErr := importInput([]string{"", "缺少编码"}, headers, "00000000-0000-0000-0000-000000000001")
	if rowErr == nil || rowErr.code != "missing_code" {
		t.Fatalf("unexpected row error: %#v", rowErr)
	}
}

func TestImportInputRejectsInvalidRecordCountAndSecurityCombination(t *testing.T) {
	headers := mapHeaders([]string{"系统编码", "系统名称", "个人信息记录数", "是否包含个人信息", "是否包含敏感个人信息"})
	_, rowErr := importInput([]string{"CRM_002", "客户系统", "12.5", "是", "否"}, headers, "00000000-0000-0000-0000-000000000001")
	if rowErr == nil || rowErr.code != "invalid_record_count" {
		t.Fatalf("unexpected record count error: %#v", rowErr)
	}
	_, rowErr = importInput([]string{"CRM_003", "客户系统", "10", "否", "是"}, headers, "00000000-0000-0000-0000-000000000001")
	if rowErr == nil || rowErr.code != "invalid_fields" {
		t.Fatalf("unexpected security error: %#v", rowErr)
	}
}

func TestTemplateSupportsCSVAndRejectsUnknownFormat(t *testing.T) {
	data, contentType, fileName, err := Template("csv")
	if err != nil || len(data) == 0 || contentType == "" || fileName != "业务系统导入模板.csv" {
		t.Fatalf("unexpected csv template: size=%d contentType=%q fileName=%q err=%v", len(data), contentType, fileName, err)
	}
	if _, _, _, err := Template("xls"); err != ErrInvalid {
		t.Fatalf("Template(xls) error = %v", err)
	}
}

func TestBuildBusinessSystemExportCSV(t *testing.T) {
	recordCount := int64(120000)
	createdAt := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	data, contentType, fileName, err := buildExport([]BusinessSystem{{
		OrganizationCode: "SUB_A", OrganizationName: "甲公司", Code: "CRM_001", Name: "客户系统", ShortName: "CRM",
		Status: "running", Level: "important", OwnerName: "张三", OwnerContact: "13800000000", InternetExposure: "partial",
		ContainsPersonalInformation: "yes", ContainsSensitivePersonalInformation: "no", PersonalInformationRecordCount: &recordCount,
		DataVolumeOver100K: "yes", ContainsImportantData: "pending", ContainsCoreData: "no", DataStorageLocation: "上海",
		CreatedAt: createdAt,
	}}, "csv")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv; charset=utf-8" || fileName != "业务系统台账.csv" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][5] != "运行中" || rows[1][6] != "重要" || rows[1][9] != "部分" {
		t.Fatalf("unexpected base fields: %#v", rows)
	}
	if rows[1][12] != "是" || rows[1][13] != "否" || rows[1][14] != "120000" || rows[1][15] != "是" || rows[1][16] != "待认定" || rows[1][17] != "否" {
		t.Fatalf("unexpected data security fields: %#v", rows[1])
	}
	if rows[1][20] != "2026-07-22 09:00:00Z" {
		t.Fatalf("unexpected exported time: %q", rows[1][20])
	}
}

func TestBuildBusinessSystemExportXLSXAndRejectInvalidFormat(t *testing.T) {
	data, _, fileName, err := buildExport([]BusinessSystem{{OrganizationCode: "SUB_A", OrganizationName: "甲公司", Code: "ERP_001", Name: "ERP", Status: "planning"}}, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if fileName != "业务系统台账.xlsx" {
		t.Fatalf("unexpected file name: %q", fileName)
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.GetRows("台账")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][2] != "系统编码" || rows[1][5] != "规划中" {
		t.Fatalf("unexpected XLSX rows: %#v", rows)
	}
	if _, _, _, err := buildExport(nil, "xls"); err != ErrInvalid {
		t.Fatalf("buildExport(xls) error = %v", err)
	}
}

func TestBusinessSystemFieldChangesIncludesSecurityFields(t *testing.T) {
	oldCount := int64(50000)
	newCount := int64(120000)
	current := BusinessSystem{
		Code: "CRM_001", Name: "客户系统", OrganizationName: "甲公司", Status: "running", Level: "normal", InternetExposure: "unknown",
		ContainsPersonalInformation: "yes", ContainsSensitivePersonalInformation: "no", PersonalInformationRecordCount: &oldCount,
		DataVolumeOver100K: "no", ContainsImportantData: "unknown", ContainsCoreData: "no",
	}
	input := validBusinessSystemInput()
	input.Code, input.Name, input.OrganizationID = "CRM_002", "客户系统二期", "org-b"
	input.Status, input.Level, input.InternetExposure = "suspended", "important", "partial"
	input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation = "yes", "yes"
	input.PersonalInformationRecordCount, input.DataVolumeOver100K = &newCount, "yes"
	input.ContainsImportantData, input.ContainsCoreData = "pending", "yes"
	input.DataStorageLocation, input.SecurityNotes = "上海", "需加密"
	changes := businessSystemFieldChanges(current, input, "乙公司")
	if len(changes) < 12 {
		t.Fatalf("changes count = %d, want at least 12: %#v", len(changes), changes)
	}
	found := map[string]fieldChange{}
	for _, change := range changes {
		found[change.Field] = change
	}
	if found["personal_information_record_count"].Before != "50000" || found["personal_information_record_count"].After != "120000" {
		t.Fatalf("record count change = %#v", found["personal_information_record_count"])
	}
	if found["data_volume_over_100k"].Before != "否" || found["data_volume_over_100k"].After != "是" {
		t.Fatalf("data volume change = %#v", found["data_volume_over_100k"])
	}
	if found["system_status"].Before != "运行中" || found["system_status"].After != "暂停" {
		t.Fatalf("status change = %#v", found["system_status"])
	}
}

func TestBusinessSystemFieldChangesSkipsUnchangedFields(t *testing.T) {
	input := validBusinessSystemInput()
	current := BusinessSystem{Code: input.Code, Name: input.Name, OrganizationName: "甲公司", Status: input.Status, Level: input.Level, InternetExposure: input.InternetExposure, ContainsPersonalInformation: input.ContainsPersonalInformation, ContainsSensitivePersonalInformation: input.ContainsSensitivePersonalInformation, DataVolumeOver100K: input.DataVolumeOver100K, ContainsImportantData: input.ContainsImportantData, ContainsCoreData: input.ContainsCoreData}
	if changes := businessSystemFieldChanges(current, input, "甲公司"); len(changes) != 0 {
		t.Fatalf("changes = %#v, want empty", changes)
	}
}

func TestNormalizeBatchIDsDeduplicatesAndValidatesLimit(t *testing.T) {
	ids, err := normalizeBatchIDs([]string{" system-a ", "system-b", "system-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "system-a" || ids[1] != "system-b" {
		t.Fatalf("unexpected normalized ids: %#v", ids)
	}
	if _, err := normalizeBatchIDs(nil); err != ErrInvalid {
		t.Fatalf("empty batch error = %v, want ErrInvalid", err)
	}
	tooMany := make([]string, MaxBatchItems+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("system-%d", index)
	}
	if _, err := normalizeBatchIDs(tooMany); err != ErrInvalid {
		t.Fatalf("oversized batch error = %v, want ErrInvalid", err)
	}
}

func TestBusinessSystemBatchErrorMessages(t *testing.T) {
	code, message := businessSystemBatchError(ErrForbidden, "归档")
	if code != "forbidden" || message != "当前账号无权归档该业务系统" {
		t.Fatalf("unexpected forbidden result: %q %q", code, message)
	}
	code, message = businessSystemBatchError(ErrNotFound, "恢复")
	if code != "not_found" || message != "业务系统不存在" {
		t.Fatalf("unexpected not found result: %q %q", code, message)
	}
}

func TestBuildBusinessSystemExportRejectsOversizedSynchronousXLSX(t *testing.T) {
	items := make([]BusinessSystem, exportfile.MaxSynchronousXLSXRows+1)
	if _, _, _, err := buildExport(items, "xlsx"); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("buildExport oversized xlsx error = %v, want ErrExportTooLarge", err)
	}
}

func TestPrepareCreateInputNormalizesAndDerivesDataVolume(t *testing.T) {
	count := int64(100000)
	input := validBusinessSystemInput()
	input.Code = "  CRM_100  "
	input.Name = "  客户服务系统  "
	input.OwnerContact = "  owner@example.com  "
	input.PersonalInformationRecordCount = &count

	if err := PrepareCreateInput(&input); err != nil {
		t.Fatalf("PrepareCreateInput() error = %v", err)
	}
	if input.Code != "CRM_100" || input.Name != "客户服务系统" || input.OwnerContact != "owner@example.com" {
		t.Fatalf("normalized input = %#v", input)
	}
	if input.DataVolumeOver100K != "yes" {
		t.Fatalf("DataVolumeOver100K = %q, want yes", input.DataVolumeOver100K)
	}

	count = 99999
	input = validBusinessSystemInput()
	input.PersonalInformationRecordCount = &count
	input.DataVolumeOver100K = "yes"
	if err := PrepareCreateInput(&input); err != nil {
		t.Fatalf("PrepareCreateInput() below threshold error = %v", err)
	}
	if input.DataVolumeOver100K != "no" {
		t.Fatalf("DataVolumeOver100K = %q, want no", input.DataVolumeOver100K)
	}
}

func TestPrepareCreateInputRejectsInvalidCombination(t *testing.T) {
	input := validBusinessSystemInput()
	input.ContainsPersonalInformation = "no"
	input.ContainsSensitivePersonalInformation = "yes"
	if err := PrepareCreateInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PrepareCreateInput() error = %v, want ErrInvalid", err)
	}
}
