package exportfile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestBuildCSVIncludesBOMAndRows(t *testing.T) {
	data, contentType, fileName, err := Build("csv", "测试台账", []string{"名称", "备注"}, [][]string{{"南博集团", "包含,逗号"}})
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv; charset=utf-8" || fileName != "测试台账.csv" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	if !bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")) {
		t.Fatal("CSV does not include UTF-8 BOM")
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "名称" || rows[1][0] != "南博集团" || rows[1][1] != "包含,逗号" {
		t.Fatalf("unexpected CSV rows: %#v", rows)
	}
}

func TestBuildXLSXIncludesStyledLedger(t *testing.T) {
	data, contentType, fileName, err := Build("xlsx", "测试台账", []string{"组织", "资产"}, [][]string{{"南博集团", "portal.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contentType, "spreadsheetml") || fileName != "测试台账.xlsx" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
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
	if len(rows) != 2 || rows[0][0] != "组织" || rows[1][1] != "portal.example.com" {
		t.Fatalf("unexpected XLSX rows: %#v", rows)
	}
	styleID, err := file.GetCellStyle("台账", "A1")
	if err != nil || styleID == 0 {
		t.Fatalf("header style was not applied: style=%d err=%v", styleID, err)
	}
}

func TestBuildRejectsUnknownFormat(t *testing.T) {
	_, _, _, err := Build("xls", "测试台账", nil, nil)
	if !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("Build(xls) error = %v", err)
	}
}

func TestBuildRejectsOversizedSynchronousXLSX(t *testing.T) {
	rows := make([][]string, MaxSynchronousXLSXRows+1)
	for index := range rows {
		rows[index] = []string{"asset"}
	}
	_, _, _, err := Build("xlsx", "测试台账", []string{"资产"}, rows)
	if !errors.Is(err, ErrSynchronousXLSXTooLarge) {
		t.Fatalf("Build oversized xlsx error = %v, want ErrSynchronousXLSXTooLarge", err)
	}
	if err := ValidateSynchronousExport("csv", len(rows)); err != nil {
		t.Fatalf("CSV should remain available for %d rows: %v", len(rows), err)
	}
}

type flushingBuffer struct {
	bytes.Buffer
	flushCount int
}

func (b *flushingBuffer) Flush() { b.flushCount++ }

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCSVStreamWritesProgressivelyAndFlushes(t *testing.T) {
	output := &flushingBuffer{}
	stream, err := NewCSVStream(output, []string{"资产", "备注"})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Write([]string{"portal.example.com", "包含,逗号"}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.flushCount != 2 {
		t.Fatalf("flush count = %d, want 2", output.flushCount)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(output.Bytes(), []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "资产" || rows[1][0] != "portal.example.com" || rows[1][1] != "包含,逗号" {
		t.Fatalf("unexpected streamed CSV rows: %#v", rows)
	}
}

func TestCSVStreamReturnsDestinationWriteError(t *testing.T) {
	want := errors.New("connection closed")
	if _, err := NewCSVStream(failingWriter{err: want}, []string{"资产"}); !errors.Is(err, want) {
		t.Fatalf("NewCSVStream error = %v, want %v", err, want)
	}
}
