// Package document defines the field-independent contract used by importers,
// HTTP clients and analyzers. Rental questions are data, not struct members.
package document

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion  = 1
	MaxFields      = 500
	MaxLabelLength = 256
	MaxValueLength = 10000
)

type Field struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value *string `json:"value"`
	Sheet string  `json:"sheet"`
	Cell  string  `json:"cell"`
}

type Document struct {
	SchemaVersion int     `json:"schemaVersion"`
	FileName      string  `json:"fileName"`
	Fields        []Field `json:"fields"`
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的数据版本，请重新上传表格")
	}
	if len(d.Fields) == 0 || len(d.Fields) > MaxFields {
		return fmt.Errorf("表格需要包含 1 至 %d 个字段", MaxFields)
	}
	ids := make(map[string]bool, len(d.Fields))
	for _, f := range d.Fields {
		if strings.TrimSpace(f.ID) == "" || len(f.ID) > 128 || ids[f.ID] {
			return fmt.Errorf("字段标识无效或重复，请重新上传表格")
		}
		ids[f.ID] = true
		if strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > MaxLabelLength {
			return fmt.Errorf("字段名不能为空，且不能超过 %d 个字符", MaxLabelLength)
		}
		if f.Value != nil && utf8.RuneCountInString(*f.Value) > MaxValueLength {
			return fmt.Errorf("单个填写值不能超过 %d 个字符", MaxValueLength)
		}
		if f.Sheet == "" || utf8.RuneCountInString(f.Sheet) > 31 || f.Cell == "" || len(f.Cell) > 16 {
			return fmt.Errorf("字段来源位置无效，请重新上传表格")
		}
	}
	return nil
}
