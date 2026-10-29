package remediation

func validateMaterial(material Material) error {
	for _, field := range []struct {
		value    string
		limit    int
		required bool
		message  string
	}{
		{material.Title, 255, true, "发现标题：请输入 1 至 255 字"},
		{material.Description, 20000, true, "发现描述：请输入 1 至 20000 字"},
		{material.Requirement, 20000, true, "整改要求：请输入 1 至 20000 字"},
		{material.Evidence, 20000, false, "发现证据：最多 20000 字"},
		{material.Advice, 20000, false, "整改建议：最多 20000 字"},
		{material.AffectedLocation, 2000, false, "影响位置：最多 2000 字"},
	} {
		if !textOK(field.value, field.limit, field.required) {
			return invalidField(field.message)
		}
	}
	if !contains([]string{"critical", "high", "medium", "low", "info"}, material.Severity) {
		return invalidField("严重程度：请选择有效的严重程度")
	}
	return nil
}

func validateActionFields(input ActionInput) error {
	for _, field := range []struct {
		value   string
		limit   int
		message string
	}{
		{input.BlockedDependency, 2000, "阻塞依赖：最多 2000 字"},
		{input.VerifiedEnvironment, 255, "验证环境：最多 255 字"},
		{input.NoDeploymentReason, 2000, "无需部署原因：最多 2000 字"},
		{input.Reason, 20000, "处理说明：最多 20000 字"},
		{input.InternalNote, 20000, "内部备注：最多 20000 字"},
		{input.Progress, 5000, "整改进度：最多 5000 字"},
		{input.BlockedReason, 2000, "阻塞原因：最多 2000 字"},
	} {
		if !textOK(field.value, field.limit, false) {
			return invalidField(field.message)
		}
	}
	return nil
}
