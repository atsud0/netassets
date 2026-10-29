package remediation

import "netassets/internal/textlength"

func validatePersonPassword(password string) error {
	if textlength.Count(password) < 10 {
		return invalidField("密码：至少 10 位")
	}
	if len(password) > 72 {
		return invalidField("密码：最多 72 字节")
	}
	return nil
}
