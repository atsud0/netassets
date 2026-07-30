package exportfile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

var (
	ErrInvalidFormat           = errors.New("invalid export format")
	ErrSynchronousXLSXTooLarge = errors.New("synchronous xlsx export exceeds row limit")
)

// MaxSynchronousXLSXRows keeps synchronous spreadsheet generation bounded.
// Larger ledgers must use CSV until the asynchronous export-job capability is enabled.
const (
	MaxSynchronousXLSXRows = 5000
	CSVContentType         = "text/csv; charset=utf-8"
)

// CSVStream writes a UTF-8 BOM CSV directly to its destination. Call Flush
// after each bounded result page so HTTP clients receive data progressively and
// the process never needs to retain the complete export in memory.
type Metadata struct {
	ContentType string
	FileName    string
	Count       int
}

type ReadyFunc func(Metadata)

type ProgressFunc func(total, processed int) error

// XLSXStream writes a workbook to disk using excelize's streaming worksheet
// writer. Only the current row is retained by the caller, which keeps large
// background exports from building a complete [][]string or byte buffer.
type XLSXStream struct {
	file      *excelize.File
	writer    *excelize.StreamWriter
	path      string
	nextRow   int
	finalized bool
}

func NewXLSXStream(path, sheet string, headers []string) (*XLSXStream, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("xlsx output path is required")
	}
	if sheet = strings.TrimSpace(sheet); sheet == "" {
		sheet = "台账"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file := excelize.NewFile()
	original := file.GetSheetName(0)
	if err := file.SetSheetName(original, sheet); err != nil {
		_ = file.Close()
		return nil, err
	}
	writer, err := file.NewStreamWriter(sheet)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	stream := &XLSXStream{file: file, writer: writer, path: path, nextRow: 2}
	for column, header := range headers {
		width := displayWidth(header) + 4
		if width < 12 {
			width = 12
		}
		if width > 32 {
			width = 32
		}
		if err := writer.SetColWidth(column+1, column+1, float64(width)); err != nil {
			stream.Abort()
			return nil, err
		}
	}
	headerStyle, err := file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "102A43"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"DCEAF3"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		stream.Abort()
		return nil, err
	}
	headerRow := make([]interface{}, len(headers))
	for index, value := range headers {
		headerRow[index] = excelize.Cell{StyleID: headerStyle, Value: value}
	}
	if err := writer.SetRow("A1", headerRow); err != nil {
		stream.Abort()
		return nil, err
	}
	if len(headers) > 0 {
		lastColumn, _ := excelize.ColumnNumberToName(len(headers))
		_ = file.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		_ = file.AutoFilter(sheet, "A1:"+lastColumn+"1", nil)
	}
	return stream, nil
}

func (s *XLSXStream) Write(row []string) error {
	if s == nil || s.finalized {
		return errors.New("xlsx stream is closed")
	}
	values := make([]interface{}, len(row))
	for index, value := range row {
		values[index] = value
	}
	cell, err := excelize.CoordinatesToCellName(1, s.nextRow)
	if err != nil {
		return err
	}
	if err := s.writer.SetRow(cell, values); err != nil {
		return err
	}
	s.nextRow++
	return nil
}

func (s *XLSXStream) Close() error {
	if s == nil || s.finalized {
		return nil
	}
	s.finalized = true
	if err := s.writer.Flush(); err != nil {
		_ = s.file.Close()
		_ = os.Remove(s.path)
		return err
	}
	if err := s.file.SaveAs(s.path); err != nil {
		_ = s.file.Close()
		_ = os.Remove(s.path)
		return err
	}
	return s.file.Close()
}

func (s *XLSXStream) Abort() {
	if s == nil || s.finalized {
		return
	}
	s.finalized = true
	_ = s.file.Close()
	_ = os.Remove(s.path)
}

type CSVStream struct {
	writer  *csv.Writer
	flusher interface{ Flush() }
}

func NewCSVStream(output io.Writer, headers []string) (*CSVStream, error) {
	if _, err := io.WriteString(output, "\xEF\xBB\xBF"); err != nil {
		return nil, err
	}
	stream := &CSVStream{writer: csv.NewWriter(output)}
	if flusher, ok := output.(interface{ Flush() }); ok {
		stream.flusher = flusher
	}
	if err := stream.Write(headers); err != nil {
		return nil, err
	}
	if err := stream.Flush(); err != nil {
		return nil, err
	}
	return stream, nil
}

func (s *CSVStream) Write(row []string) error {
	if err := s.writer.Write(row); err != nil {
		return err
	}
	return s.writer.Error()
}

func (s *CSVStream) Flush() error {
	s.writer.Flush()
	if err := s.writer.Error(); err != nil {
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// ValidateSynchronousExport verifies the requested format and bounds memory-heavy
// spreadsheet exports performed in the HTTP request path.
func ValidateSynchronousExport(format string, rowCount int) error {
	switch normalizeFormat(format) {
	case "csv":
		return nil
	case "xlsx", "":
		if rowCount > MaxSynchronousXLSXRows {
			return ErrSynchronousXLSXTooLarge
		}
		return nil
	default:
		return ErrInvalidFormat
	}
}

func normalizeFormat(format string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
}

// Build creates a UTF-8 CSV or XLSX workbook from ledger rows.
func Build(format, baseName string, headers []string, rows [][]string) ([]byte, string, string, error) {
	format = normalizeFormat(format)
	if err := ValidateSynchronousExport(format, len(rows)); err != nil {
		return nil, "", "", err
	}
	switch format {
	case "csv":
		var buffer bytes.Buffer
		stream, err := NewCSVStream(&buffer, headers)
		if err != nil {
			return nil, "", "", err
		}
		for _, row := range rows {
			if err := stream.Write(row); err != nil {
				return nil, "", "", err
			}
		}
		if err := stream.Flush(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), CSVContentType, baseName + ".csv", nil
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
