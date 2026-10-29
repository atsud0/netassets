package businesssystem

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareBusinessSystemInputFieldMessages(t *testing.T) {
	negative := int64(-1)
	for _, tc := range []struct {
		name    string
		change  func(*CreateInput)
		message string
	}{
		{"code", func(in *CreateInput) { in.Code = "?" }, "系统编码须为 2–64 位字母、数字、下划线或横线，且以字母或数字开头"},
		{"blank name", func(in *CreateInput) { in.Name = "　 " }, "系统名称不能为空"},
		{"long name", func(in *CreateInput) { in.Name = strings.Repeat("系", 256) }, "系统名称最多 255 字"},
		{"organization", func(in *CreateInput) { in.OrganizationID = " " }, "请选择所属组织"},
		{"status", func(in *CreateInput) { in.Status = "bad" }, "请选择有效的系统状态"},
		{"level", func(in *CreateInput) { in.Level = "bad" }, "请选择有效的系统等级"},
		{"internet exposure", func(in *CreateInput) { in.InternetExposure = "bad" }, "请选择有效的互联网暴露情况"},
		{"personal", func(in *CreateInput) { in.ContainsPersonalInformation = "bad" }, "请选择有效的是否包含个人信息选项"},
		{"sensitive", func(in *CreateInput) { in.ContainsSensitivePersonalInformation = "bad" }, "请选择有效的是否包含敏感个人信息选项"},
		{"contradiction", func(in *CreateInput) {
			in.ContainsPersonalInformation = "no"
			in.ContainsSensitivePersonalInformation = "yes"
		}, "包含敏感个人信息时，是否包含个人信息不能选择否"},
		{"negative count", func(in *CreateInput) { in.PersonalInformationRecordCount = &negative }, "个人信息记录数量不能小于 0"},
		{"volume", func(in *CreateInput) { in.DataVolumeOver100K = "bad" }, "请选择有效的数据量是否超过 10 万条选项"},
		{"important", func(in *CreateInput) { in.ContainsImportantData = "bad" }, "请选择有效的是否包含重要数据选项"},
		{"core", func(in *CreateInput) { in.ContainsCoreData = "bad" }, "请选择有效的是否包含核心数据选项"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validBusinessSystemInput()
			tc.change(&input)
			err := PrepareCreateInput(&input)
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}
