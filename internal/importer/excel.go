package importer

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/xuri/excelize/v2"
)

const MaxFileBytes int64 = 10 << 20
const maxRows = 2000

type Result struct {
	Document document.Document `json:"document"`
	Warnings []string          `json:"warnings"`
}

// Parse accepts the two-column form, without a whitelist of rental questions.
// Each imported document is a snapshot. Duplicate labels remain separate fields.
func Parse(ctx context.Context, reader io.Reader, fileName string) (Result, error) {
	result := Result{Warnings: []string{}}
	f, err := excelize.OpenReader(reader, excelize.Options{
		UnzipSizeLimit: 32 << 20, UnzipXMLSizeLimit: 8 << 20,
	})
	if err != nil {
		return result, fmt.Errorf("无法读取 Excel，请确认文件是未加密、未损坏的 .xlsx 表格")
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) > 10 {
		return result, fmt.Errorf("工作表数量过多，请只保留本次房源的表格")
	}
	var fields []document.Field
	for _, sheet := range sheets {
		parsed, err := parseSheet(ctx, f, sheet)
		if err != nil {
			return result, err
		}
		if len(parsed) == 0 {
			continue
		}
		if len(fields) != 0 {
			return result, fmt.Errorf("检测到多张有内容的工作表，请每次上传一套房源的一张表")
		}
		fields = parsed
	}
	result.Document = document.Document{
		SchemaVersion: document.SchemaVersion,
		FileName:      path.Base(strings.ReplaceAll(fileName, "\\", "/")),
		Fields:        fields,
	}
	if err := result.Document.Validate(); err != nil {
		return result, err
	}
	counts := map[string]int{}
	for _, field := range fields {
		counts[field.Label]++
		if counts[field.Label] == 2 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("“%s”出现多次，已按原表分别保留，请核对。", field.Label))
		}
	}
	return result, nil
}

func parseSheet(ctx context.Context, f *excelize.File, sheet string) ([]document.Field, error) {
	merges, err := f.GetMergeCells(sheet)
	if err != nil {
		return nil, fmt.Errorf("无法读取工作表“%s”", sheet)
	}
	if len(merges) > 0 {
		return nil, fmt.Errorf("工作表“%s”含合并单元格，请整理为 A 列项目、B 列填写值的两列表格", sheet)
	}
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("无法读取工作表“%s”", sheet)
	}
	defer rows.Close()
	fields := []document.Field{}
	firstContent := true
	rowNumber := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rowNumber++
		if rowNumber > maxRows {
			return nil, fmt.Errorf("工作表超过 %d 行，请移除多余行", maxRows)
		}
		row, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("无法读取 %s 第 %d 行", sheet, rowNumber)
		}
		// Formula caches may be empty or stale. Never pass them off as current facts.
		for col := 1; col <= max(2, len(row)); col++ {
			cell, _ := excelize.CoordinatesToCellName(col, rowNumber)
			formula, err := f.GetCellFormula(sheet, cell)
			if err != nil {
				return nil, fmt.Errorf("无法读取 %s!%s", sheet, cell)
			}
			if formula != "" {
				return nil, fmt.Errorf("%s!%s 使用了公式，请确认结果后粘贴为数值，再上传", sheet, cell)
			}
		}
		for col := 2; col < len(row); col++ {
			if strings.TrimSpace(row[col]) != "" {
				return nil, fmt.Errorf("%s 第 %d 行在 B 列之后还有内容，请使用两列表格，避免遗漏信息", sheet, rowNumber)
			}
		}
		label, value := "", ""
		if len(row) > 0 {
			label = strings.TrimSpace(row[0])
		}
		if len(row) > 1 {
			value = row[1]
		}
		if label == "" && strings.TrimSpace(value) == "" {
			continue
		}
		if firstContent && isHeader(label, strings.TrimSpace(value)) {
			firstContent = false
			continue
		}
		firstContent = false
		if label == "" {
			return nil, fmt.Errorf("%s!A%d 缺少项目名称，请补充后上传", sheet, rowNumber)
		}
		if len(fields) >= document.MaxFields {
			return nil, fmt.Errorf("字段数量不能超过 %d 项", document.MaxFields)
		}
		field := document.Field{ID: fmt.Sprintf("field-%d", len(fields)+1), Label: label, Sheet: sheet, Cell: fmt.Sprintf("B%d", rowNumber)}
		if strings.TrimSpace(value) != "" {
			field.Value = &value
		}
		fields = append(fields, field)
	}
	if rows.Error() != nil {
		return nil, fmt.Errorf("读取工作表“%s”失败", sheet)
	}
	return fields, nil
}

func isHeader(label, value string) bool {
	labelOK := label == "" || label == "项目" || label == "字段" || label == "字段名"
	valueOK := value == "数值" || value == "值" || value == "内容" || value == "填写值"
	return labelOK && valueOK
}
