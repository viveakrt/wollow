// Minimal OOXML (.xlsx) reading.
//
// This is not a general spreadsheet reader: no formulas, no merged cells, no
// number formats, no styles — just enough of the zip/XML structure to pull
// flat data tables out of a workbook, which is all a Zerodha Console export
// (see zerodha_pnl.go) is. That keeps this dependency-free: a .xlsx file is
// already a zip of XML, and archive/zip plus encoding/xml is all it takes.
package parsers

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// xlsxSheet is one worksheet's cell grid, keyed by the workbook's own 0-based
// row/column numbers. Cells the export left genuinely empty are simply
// absent, so callers ask for a bounded row rather than iterate a fixed width.
type xlsxSheet struct {
	rows map[int]map[int]string
}

// row returns the cells of row r as a slice from column 0 through the last
// non-empty column, so callers can index it the way they would a real row.
// An entirely empty (or absent) row returns nil.
func (s *xlsxSheet) row(r int) []string {
	cols := s.rows[r]
	maxCol := -1
	for c := range cols {
		if c > maxCol {
			maxCol = c
		}
	}
	if maxCol < 0 {
		return nil
	}
	out := make([]string, maxCol+1)
	for c, v := range cols {
		out[c] = v
	}
	return out
}

// maxRow is the highest row number the sheet has any content on.
func (s *xlsxSheet) maxRow() int {
	max := -1
	for r := range s.rows {
		if r > max {
			max = r
		}
	}
	return max
}

type xlsxSSTXML struct {
	SI []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type xlsxRelsXML struct {
	Relationship []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type xlsxWorkbookXML struct {
	Sheets struct {
		Sheet []struct {
			Name string `xml:"name,attr"`
			// RID is workbook.xml's r:id attribute; encoding/xml matches it by
			// local name ("id") regardless of the "r" namespace prefix.
			RID string `xml:"id,attr"`
		} `xml:"sheet"`
	} `xml:"sheets"`
}

type xlsxSheetDataXML struct {
	Row []struct {
		R string `xml:"r,attr"`
		C []struct {
			R  string `xml:"r,attr"`
			T  string `xml:"t,attr"`
			V  string `xml:"v"`
			Is struct {
				T string `xml:"t"`
			} `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

// xlsxColToIndex converts a cell reference's column letters ("BC12" -> "BC")
// to a 0-based column index.
func xlsxColToIndex(cellRef string) int {
	idx := 0
	for _, ch := range cellRef {
		if ch < 'A' || ch > 'Z' {
			break
		}
		idx = idx*26 + int(ch-'A'+1)
	}
	return idx - 1
}

// openXLSXSheets reads every worksheet out of an OOXML workbook, keyed by the
// sheet's own tab name — Console names its sheets "Equity" / "Mutual Funds" /
// "Other Debits and Credits", and that name is the only reliable way to tell
// them apart, since the underlying sheetN.xml files are numbered by creation
// order rather than tab order.
func openXLSXSheets(path string) (map[string]*xlsxSheet, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer zr.Close()

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	shared, err := xlsxReadSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}

	var rels xlsxRelsXML
	if f, ok := files["xl/_rels/workbook.xml.rels"]; ok {
		if err := xlsxDecodeFile(f, &rels); err != nil {
			return nil, fmt.Errorf("reading workbook rels: %w", err)
		}
	}
	relTarget := map[string]string{}
	for _, r := range rels.Relationship {
		relTarget[r.ID] = r.Target
	}

	var wb xlsxWorkbookXML
	wbFile, ok := files["xl/workbook.xml"]
	if !ok {
		return nil, fmt.Errorf("workbook.xml missing from archive")
	}
	if err := xlsxDecodeFile(wbFile, &wb); err != nil {
		return nil, fmt.Errorf("reading workbook.xml: %w", err)
	}

	sheets := map[string]*xlsxSheet{}
	for _, ref := range wb.Sheets.Sheet {
		target := relTarget[ref.RID]
		if target == "" {
			continue
		}
		target = strings.TrimPrefix(target, "/xl/")
		target = strings.TrimPrefix(target, "/")
		if !strings.HasPrefix(target, "xl/") {
			target = "xl/" + target
		}
		f, ok := files[target]
		if !ok {
			continue
		}
		sheet, err := xlsxReadSheet(f, shared)
		if err != nil {
			return nil, fmt.Errorf("reading sheet %q: %w", ref.Name, err)
		}
		sheets[ref.Name] = sheet
	}
	return sheets, nil
}

func xlsxDecodeFile(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return xml.NewDecoder(rc).Decode(v)
}

func xlsxReadSharedStrings(f *zip.File) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	var sst xlsxSSTXML
	if err := xlsxDecodeFile(f, &sst); err != nil {
		return nil, fmt.Errorf("reading sharedStrings.xml: %w", err)
	}
	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		if si.T != "" {
			out[i] = si.T
			continue
		}
		var b strings.Builder
		for _, run := range si.R {
			b.WriteString(run.T)
		}
		out[i] = b.String()
	}
	return out, nil
}

func xlsxReadSheet(f *zip.File, shared []string) (*xlsxSheet, error) {
	var sd xlsxSheetDataXML
	if err := xlsxDecodeFile(f, &sd); err != nil {
		return nil, err
	}
	sheet := &xlsxSheet{rows: map[int]map[int]string{}}
	for _, row := range sd.Row {
		rowNum, err := strconv.Atoi(row.R)
		if err != nil {
			continue
		}
		cells := map[int]string{}
		for _, c := range row.C {
			if c.R == "" {
				continue
			}
			idx := xlsxColToIndex(c.R)
			var val string
			switch {
			case c.Is.T != "":
				val = c.Is.T
			case c.T == "s":
				n, err := strconv.Atoi(c.V)
				if err == nil && n >= 0 && n < len(shared) {
					val = shared[n]
				}
			default:
				val = c.V
			}
			cells[idx] = val
		}
		// Row numbers in the file are 1-based; keyed as-is so callers can use
		// the same numbering the workbook itself shows.
		sheet.rows[rowNum] = cells
	}
	return sheet, nil
}
