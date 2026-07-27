package exportfile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

var ErrInvalidFormat = errors.New("invalid export format")

// Build creates a UTF-8 CSV or XLSX workbook from ledger rows.
func Build(format, baseName string, headers []string, rows [][]string) ([]byte, string, string, error) {
	format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	switch format {
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xEF\xBB\xBF")
		writer := csv.NewWriter(&buffer)
		if err := writer.Write(headers); err != nil {
			return nil, "", "", err
		}
		writer.WriteAll(rows)
		if err := writer.Error(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "text/csv; charset=utf-8", baseName + ".csv", nil
	case "xlsx", "":
		file := excelize.NewFile()
		defer file.Close()
		sheet := file.GetSheetName(0)
		_ = file.SetSheetName(sheet, "台账")
		sheet = "台账"
		for column, value := range headers {
			cell, _ := excelize.CoordinatesToCellName(column+1, 1)
			if err := file.SetCellValue(sheet, cell, value); err != nil {
				return nil, "", "", err
			}
		}
		for rowIndex, row := range rows {
			for column, value := range row {
				cell, _ := excelize.CoordinatesToCellName(column+1, rowIndex+2)
				if err := file.SetCellValue(sheet, cell, value); err != nil {
					return nil, "", "", err
				}
			}
		}
		if len(headers) > 0 {
			lastColumn, _ := excelize.ColumnNumberToName(len(headers))
			headerStyle, err := file.NewStyle(&excelize.Style{
				Font:      &excelize.Font{Bold: true, Color: "102A43"},
				Fill:      excelize.Fill{Type: "pattern", Color: []string{"DCEAF3"}, Pattern: 1},
				Alignment: &excelize.Alignment{Vertical: "center"},
			})
			if err != nil {
				return nil, "", "", err
			}
			if err := file.SetCellStyle(sheet, "A1", lastColumn+"1", headerStyle); err != nil {
				return nil, "", "", err
			}
			_ = file.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
			_ = file.AutoFilter(sheet, "A1:"+lastColumn+"1", nil)
			for column := range headers {
				width := displayWidth(headers[column]) + 3
				for _, row := range rows {
					if column < len(row) && displayWidth(row[column])+2 > width {
						width = displayWidth(row[column]) + 2
					}
				}
				if width < 12 {
					width = 12
				}
				if width > 42 {
					width = 42
				}
				columnName, _ := excelize.ColumnNumberToName(column + 1)
				_ = file.SetColWidth(sheet, columnName, columnName, float64(width))
			}
		}
		var buffer bytes.Buffer
		if err := file.Write(&buffer); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", baseName + ".xlsx", nil
	default:
		return nil, "", "", ErrInvalidFormat
	}
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		if r > 127 {
			width += 2
		} else {
			width++
		}
	}
	if width == 0 && value != "" {
		return utf8.RuneCountInString(value)
	}
	return width
}
