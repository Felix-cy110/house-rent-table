package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/Felix-cy110/house-rent-table/internal/testxlsx"
	"github.com/xuri/excelize/v2"
)

func TestDynamicFieldsPreserveValuesAndSources(t *testing.T) {
	data := testxlsx.Bytes(t, [][]any{
		{nil, "数值"}, {"未来新增的停车费", 0}, {}, {"月租", "2800元/月"},
		{"押几付几", "押一付三"}, {"待确认项目", nil}, {"月租", "另一个报价"},
		{"备注", "  原文保留\n第二行  "},
	})
	result, err := Parse(context.Background(), bytes.NewReader(data), "C:\\incoming\\房源.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	fields := result.Document.Fields
	if len(fields) != 6 || result.Document.FileName != "房源.xlsx" {
		t.Fatalf("unexpected document: %+v", result.Document)
	}
	if fields[0].Label != "未来新增的停车费" || fields[0].Value == nil || *fields[0].Value != "0" {
		t.Fatal("zero or unknown field was lost")
	}
	if fields[1].Cell != "B4" || fields[1].Sheet != "Sheet1" || *fields[1].Value != "2800元/月" {
		t.Fatal("source position or original text was changed")
	}
	if fields[3].Value != nil {
		t.Fatal("blank input must remain null")
	}
	if fields[1].ID == fields[4].ID || *fields[4].Value != "另一个报价" || len(result.Warnings) != 1 {
		t.Fatal("duplicate labels were overwritten")
	}
	if *fields[5].Value != "  原文保留\n第二行  " {
		t.Fatal("raw text was trimmed")
	}
	encoded, err := json.Marshal(result.Document)
	if err != nil {
		t.Fatal(err)
	}
	var restored document.Document
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Fields[3].Value != nil || *restored.Fields[0].Value != "0" {
		t.Fatal("JSON round-trip lost zero/blank distinction")
	}
}

func TestChangesDoNotRequireKnownLabels(t *testing.T) {
	for _, rows := range [][][]any{
		{{"项目", "数值"}, {"新字段", "保留"}, {"房租", "2000"}},
		{{"项目", "数值"}, {"房租", "2000"}, {"又一个新字段", "任意内容"}},
		{{"任意名称", "仅剩这一项"}},
		{{}, {nil, "数值"}, {"月租", "已改名"}},
	} {
		result, err := Parse(context.Background(), bytes.NewReader(testxlsx.Bytes(t, rows)), "test.xlsx")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Document.Fields) < 1 {
			t.Fatal("expected dynamic fields")
		}
	}
}

func TestUnsupportedContentIsNotSilentlyDropped(t *testing.T) {
	tests := []struct {
		name    string
		rows    [][]any
		edit    func(*excelize.File)
		message string
	}{
		{"empty", nil, nil, "1 至"},
		{"missing label", [][]any{{nil, "2800"}}, nil, "缺少项目名称"},
		{"extra column", [][]any{{"月租", "2000", "不能漏掉"}}, nil, "B 列之后"},
		{"formula", [][]any{{"首月总额", nil}}, func(f *excelize.File) {
			if err := f.SetCellFormula("Sheet1", "B1", "SUM(1,2)"); err != nil {
				t.Fatal(err)
			}
		}, "公式"},
		{"merged", [][]any{{"月租", "2000"}}, func(f *excelize.File) {
			if err := f.MergeCell("Sheet1", "A2", "B2"); err != nil {
				t.Fatal(err)
			}
		}, "合并单元格"},
		{"multiple sheets", [][]any{{"月租", "2000"}}, func(f *excelize.File) {
			if _, err := f.NewSheet("另一套房"); err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue("另一套房", "A1", "月租"); err != nil {
				t.Fatal(err)
			}
		}, "多张"},
		{"long label", [][]any{{strings.Repeat("名", 257), "x"}}, nil, "字段名"},
		{"long value", [][]any{{"备注", strings.Repeat("字", 10001)}}, nil, "填写值"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var edits []func(*excelize.File)
			if tt.edit != nil {
				edits = append(edits, tt.edit)
			}
			_, err := Parse(context.Background(), bytes.NewReader(testxlsx.Bytes(t, tt.rows, edits...)), "test.xlsx")
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("want %q, got %v", tt.message, err)
			}
		})
	}
}

func TestEmptyExtraSheetAndFormattedNumber(t *testing.T) {
	data := testxlsx.Bytes(t, [][]any{{"月租", 2800.5}}, func(f *excelize.File) {
		if _, err := f.NewSheet("空白页"); err != nil {
			t.Fatal(err)
		}
		format := `0.00" 元/月"`
		style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &format})
		if err != nil {
			t.Fatal(err)
		}
		if err = f.SetCellStyle("Sheet1", "B1", "B1", style); err != nil {
			t.Fatal(err)
		}
	})
	result, err := Parse(context.Background(), bytes.NewReader(data), "test.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if value := *result.Document.Fields[0].Value; value != "2800.50 元/月" {
		t.Fatalf("format was lost: %q", value)
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	rows := make([][]any, document.MaxFields+1)
	for i := range rows {
		rows[i] = []any{"项目", "值"}
	}
	// No header: do not accidentally skip the first field in this boundary test.
	for i := range rows {
		rows[i][0] = "自定义项目"
	}
	data := testxlsx.Bytes(t, rows)
	if _, err := Parse(context.Background(), bytes.NewReader(data), "large.xlsx"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected field limit, got %v", err)
	}
	if _, err := Parse(context.Background(), strings.NewReader("not an xlsx"), "bad.xlsx"); err == nil {
		t.Fatal("corrupt file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, bytes.NewReader(testxlsx.Bytes(t, [][]any{{"月租", "2000"}})), "test.xlsx"); err == nil {
		t.Fatal("cancelled import continued")
	}
}

func TestRepositoryTemplate(t *testing.T) {
	paths, err := filepath.Glob("../../outputs/*/租房信息模板.xlsx")
	if err != nil || len(paths) != 1 {
		t.Fatalf("template not found: %v", err)
	}
	f, err := os.Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result, err := Parse(context.Background(), f, filepath.Base(paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range result.Document.Fields {
		if field.Value != nil {
			t.Fatal("public template must remain blank")
		}
	}
}
