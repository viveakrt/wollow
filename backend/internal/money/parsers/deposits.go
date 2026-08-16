// Deposit summary parsing.
//
// HDFC's "FD Summary" export is a different animal from its account statement:
// one row per deposit rather than per transaction, and no running balance at
// all. Layout, reverse engineered from a real export:
//
//	Row 12  : the title cell, "Fixed Deposit Summary"
//	Row 14  : header — Sr. No | Account No. | Branch | Name | CCY. |
//	          Principal Amount. | Maturity Amount | Maturity Date |
//	          Rate of Interest | Deposit Start Date
//	Row 15  : a masking row of "*"
//	Rows 17+: one deposit each
//	Then    : a "Total  INR" row, and a disclaimer block
//
// Dates here are "10 Mar 2027" — not the DD/MM/YY the account statement uses.
package parsers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/shakinm/xlsReader/xls"
	"wollow/backend/internal/money/models"
)

// depositDateLayouts covers the shapes seen in deposit exports.
var depositDateLayouts = []string{
	"02 Jan 2006", "2 Jan 2006", "02-Jan-2006", "02/01/2006", "02-01-2006",
}

func parseDepositDate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, layout := range depositDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// IsDepositSummary reports whether a workbook is a deposit summary rather than
// a transaction statement, so one upload endpoint can route either.
func IsDepositSummary(path string) bool {
	wb, err := xls.OpenFile(path)
	if err != nil {
		return false
	}
	sheet, err := wb.GetSheet(0)
	if err != nil {
		return false
	}
	if strings.Contains(strings.ToLower(sheet.GetName()), "fd summary") {
		return true
	}
	return findDepositHeaderRow(sheet) != -1
}

// findDepositHeaderRow locates the row holding the deposit table's column
// labels, identified by the two columns no transaction statement has.
func findDepositHeaderRow(sheet *xls.Sheet) int {
	limit := min(sheet.GetNumberRows(), 40)
	for r := 0; r < limit; r++ {
		row, err := sheet.GetRow(r)
		if err != nil {
			continue
		}
		var joined strings.Builder
		for _, cell := range row.GetCols() {
			joined.WriteString(strings.ToLower(cell.GetString()))
			joined.WriteString("|")
		}
		line := joined.String()
		if strings.Contains(line, "maturity amount") && strings.Contains(line, "principal amount") {
			return r
		}
	}
	return -1
}

// depositColumns maps a header label fragment to the field it fills. Positions
// are read from the header row rather than hardcoded, because HDFC ships
// slightly different column orders for FD and RD summaries.
var depositColumns = []struct {
	fragment string
	field    string
}{
	{"account no", "identifier"},
	{"branch", "branch"},
	{"name", "name"},
	{"ccy", "currency"},
	{"principal amount", "principal"},
	{"maturity amount", "maturity"},
	{"maturity date", "maturityDate"},
	{"rate of interest", "rate"},
	{"deposit start date", "startDate"},
}

// ParseDepositSummary reads a deposit summary export into holdings.
func ParseDepositSummary(path, institution string) (*models.ParsedDepositSummary, error) {
	wb, err := xls.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xls: %w", err)
	}
	sheet, err := wb.GetSheet(0)
	if err != nil {
		return nil, fmt.Errorf("get sheet: %w", err)
	}

	headerRow := findDepositHeaderRow(sheet)
	if headerRow == -1 {
		return nil, fmt.Errorf("could not locate the deposit table header (Principal/Maturity Amount columns)")
	}

	header, err := sheet.GetRow(headerRow)
	if err != nil {
		return nil, fmt.Errorf("reading header row: %w", err)
	}
	index := map[string]int{}
	for i, cell := range header.GetCols() {
		label := strings.ToLower(strings.TrimSpace(cell.GetString()))
		if label == "" {
			continue
		}
		for _, col := range depositColumns {
			if _, taken := index[col.field]; !taken && strings.Contains(label, col.fragment) {
				index[col.field] = i
				break
			}
		}
	}
	if _, ok := index["principal"]; !ok {
		return nil, fmt.Errorf("deposit table has no principal amount column")
	}

	if institution == "" {
		institution = "HDFC"
	}
	result := &models.ParsedDepositSummary{Institution: institution, Kind: "fd"}

	numRows := sheet.GetNumberRows()
	for r := headerRow + 1; r < numRows; r++ {
		row, err := sheet.GetRow(r)
		if err != nil {
			continue
		}
		identifier := strings.TrimSpace(cellString(row, index["identifier"]))
		// The masking row of asterisks, the "Total INR" row and the disclaimer
		// block all fail this: a deposit is identified by a numeric account.
		if identifier == "" || !isAllDigits(identifier) {
			if strings.Contains(strings.ToUpper(cellString(row, 0)), "END OF") {
				break
			}
			continue
		}

		principal := parseAmount(cellString(row, index["principal"]))
		if principal == 0 {
			continue
		}

		deposit := models.ParsedDeposit{
			Kind:           "fd",
			Institution:    institution,
			Identifier:     identifier,
			Branch:         strings.TrimSpace(cellString(row, index["branch"])),
			Currency:       firstNonEmptyString(strings.TrimSpace(cellString(row, index["currency"])), "INR"),
			InvestedAmount: principal,
			MaturityAmount: parseAmount(cellString(row, index["maturity"])),
			InterestRate:   parseAmount(cellString(row, index["rate"])),
			StartDate:      parseDepositDate(cellString(row, index["startDate"])),
			MaturityDate:   parseDepositDate(cellString(row, index["maturityDate"])),
		}
		deposit.Name = depositName(institution, deposit)
		deposit.DedupeKey = DepositDedupeKey(institution, identifier)
		result.Deposits = append(result.Deposits, deposit)
	}

	if len(result.Deposits) == 0 {
		return nil, fmt.Errorf("no deposits found in the summary")
	}
	return result, nil
}

// depositName labels a deposit the way a passbook would, since the export
// itself only carries the holder's name.
func depositName(institution string, d models.ParsedDeposit) string {
	label := institution + " FD"
	if d.MaturityDate != "" {
		label += " maturing " + d.MaturityDate
	}
	return label
}

// DepositDedupeKey is the stable identity of one deposit. It must not change:
// it is stored on the row and backed by a unique index, so a new formula would
// duplicate every holding on the next import.
func DepositDedupeKey(institution, identifier string) string {
	h := sha256.Sum256([]byte("deposit|" + strings.ToLower(institution) + "|" + identifier))
	return hex.EncodeToString(h[:])
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
