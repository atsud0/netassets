package securityworkorder

import "netassets/internal/textlength"

func validateWorkOrderComment(comment string, required bool) error {
	if required && comment == "" {
		return invalidField("取消原因不能为空")
	}
	if textlength.Count(comment) > 2000 {
		if required {
			return invalidField("取消原因最多 2000 字")
		}
		return invalidField("处理说明最多 2000 字")
	}
	return nil
}

func validateWorkOrderProgress(input ProgressInput) error {
	if input.Progress == "" {
		return invalidField("整改进度不能为空")
	}
	if textlength.Count(input.Progress) > 5000 {
		return invalidField("整改进度最多 5000 字")
	}
	return validateWorkOrderComment(input.Comment, false)
}

func validateWorkOrderSubmission(input SubmitInput) error {
	if input.RemediationResult == "" {
		return invalidField("整改结果不能为空")
	}
	if textlength.Count(input.RemediationResult) > 10000 {
		return invalidField("整改结果最多 10000 字")
	}
	return validateWorkOrderComment(input.Comment, false)
}

func validateWorkOrderReview(input ReviewInput) error {
	if input.Decision != "returned" && input.Decision != "closed" {
		return invalidField("请选择有效的复测结论")
	}
	if input.Decision == "returned" && input.ReturnReason == "" {
		return invalidField("退回原因不能为空")
	}
	if input.Decision == "closed" && input.VerificationResult == "" {
		return invalidField("复测结论不能为空")
	}
	if textlength.Count(input.VerificationResult) > 10000 {
		if input.Decision == "closed" {
			return invalidField("复测结论最多 10000 字")
		}
		return invalidField("复测记录最多 10000 字")
	}
	if textlength.Count(input.ReturnReason) > 2000 {
		return invalidField("退回原因最多 2000 字")
	}
	return nil
}
