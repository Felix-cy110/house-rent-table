package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/testxlsx"
	"github.com/xuri/excelize/v2"
)

func TestBoldTargetsFollowLabelsNotNamesOrValues(t *testing.T) {
	rows := [][]any{
		{"详细填写", "数值"}, {"月租", "3000"}, {"首月总共要付多少", "12000"},
		{"任意新增核算项", nil}, {"任意新增核算项", "0"}, {"仅填写值加粗", "123"},
	}
	for _, cells := range [][]string{{"A1", "A3", "A4", "B6"}, {"A1", "A5", "B6"}, {"A1", "B6"}} {
		t.Run(strings.Join(cells, ","), func(t *testing.T) {
			data := testxlsx.Bytes(t, rows, func(f *excelize.File) {
				style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
				if err != nil {
					t.Fatal(err)
				}
				for _, cell := range cells {
					if err := f.SetCellStyle("Sheet1", cell, cell, style); err != nil {
						t.Fatal(err)
					}
				}
			})
			result, err := Parse(context.Background(), bytes.NewReader(data), "标记.xlsx")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Document.Fields) != 5 {
				t.Fatal("header became a target")
			}
			for i, field := range result.Document.Fields {
				labelCell, _ := excelize.CoordinatesToCellName(1, i+2)
				want := false
				for _, cell := range cells {
					if cell == labelCell {
						want = true
					}
				}
				if field.Bold != want {
					t.Fatalf("%s: got %+v, want bold %v", labelCell, field, want)
				}
				encoded, err := json.Marshal(field)
				if err != nil {
					t.Fatal(err)
				}
				var restored map[string]any
				if err := json.Unmarshal(encoded, &restored); err != nil {
					t.Fatal(err)
				}
				if restored["bold"] != want {
					t.Fatalf("explicit boolean lost: %s", encoded)
				}
			}
			fields := result.Document.Fields
			if fields[2].Value != nil || fields[3].Value == nil || *fields[3].Value != "0" || fields[2].ID == fields[3].ID {
				t.Fatal("blank, zero or duplicate identity changed")
			}
		})
	}
}

func TestBoldFromRowColumnAndRichText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edit      func(*excelize.File, int) error
		want      bool
		wantError string
	}{
		{"row", func(f *excelize.File, style int) error { return f.SetRowStyle("Sheet1", 1, 1, style) }, true, ""},
		{"column", func(f *excelize.File, style int) error { return f.SetColStyle("Sheet1", "A", style) }, true, ""},
		{"cell overrides column", func(f *excelize.File, style int) error {
			if err := f.SetColStyle("Sheet1", "A", style); err != nil {
				return err
			}
			plain, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 12, Bold: false}})
			if err != nil {
				return err
			}
			return f.SetCellStyle("Sheet1", "A1", "A1", plain)
		}, false, ""},
		{"all rich runs bold", func(f *excelize.File, _ int) error {
			return f.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{{Text: "每月", Font: &excelize.Font{Bold: true}}, {Text: "合计", Font: &excelize.Font{Bold: true}}})
		}, true, ""},
		{"spaces do not affect bold", func(f *excelize.File, _ int) error {
			return f.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{{Text: " "}, {Text: "总额", Font: &excelize.Font{Bold: true}}, {Text: " "}})
		}, true, ""},
		{"plain rich runs override bold cell", func(f *excelize.File, style int) error {
			if err := f.SetCellStyle("Sheet1", "A1", "A1", style); err != nil {
				return err
			}
			return f.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{{Text: "总额", Font: &excelize.Font{Bold: false, Size: 12}}})
		}, false, ""},
		{"mixed rich formatting", func(f *excelize.File, _ int) error {
			return f.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{{Text: "每月", Font: &excelize.Font{Bold: true}}, {Text: "合计", Font: &excelize.Font{Bold: false}}})
		}, false, "Sheet1!A1 的项目名只有部分文字加粗"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := testxlsx.Bytes(t, [][]any{{"核算项目", "3000"}}, func(f *excelize.File) {
				style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
				if err != nil {
					t.Fatal(err)
				}
				if err := tc.edit(f, style); err != nil {
					t.Fatal(err)
				}
			})
			r, err := Parse(context.Background(), bytes.NewReader(data), "style.xlsx")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || len(r.Document.Fields) != 1 || r.Document.Fields[0].Bold != tc.want {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}
