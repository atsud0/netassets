package businesssystem

import (
	"errors"
	"fmt"

	"netassets/internal/importmapping"
)

func validateImportRows(rows [][]string, minimumRows int) error {
	if len(rows) == 0 {
		return invalidInput("导入文件：内容为空，请上传包含表头的文件")
	}
	if len(rows) < minimumRows {
		return invalidInput("导入数据：至少需要一行数据，不能只有表头")
	}
	if len(rows)-1 > MaxImportRows {
		return invalidInput(fmt.Sprintf("导入数据：最多支持 %d 行，不含表头", MaxImportRows))
	}
	return nil
}

func invalidImportMapping(err error) error {
	var validation *importmapping.ValidationError
	if errors.As(err, &validation) {
		return invalidInput(validation.Message)
	}
	return invalidInput("导入表头或列映射：配置不正确，请重新选择")
}
