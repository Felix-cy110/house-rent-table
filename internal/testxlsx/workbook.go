// Package testxlsx creates small in-memory workbooks for integration tests.
package testxlsx

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

func Bytes(t testing.TB, rows [][]any, edits ...func(*excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	for _, edit := range edits {
		edit(f)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
