// Zerodha Console P&L export parsing ("Console > Reports > P&L statement",
// downloaded as .xlsx — one workbook per instrument class, Equity or Mutual
// Funds, plus a second "Other Debits and Credits" sheet of charges this
// doesn't read).
//
// Layout, reverse engineered from real exports (both Equity and Mutual Funds
// share it — only the sheet name and which columns are non-zero differ):
//
//	Row 7        : "Client ID" | value
//	Row 11       : "P&L Statement for <Equity|Mutual Funds> from <date> to <date>"
//	Row ~13-18   : Summary block (Charges / Other Credit & Debit / Realized
//	                 P&L / Unrealized P&L totals) — not read here.
//	Row ~21-33   : Charges breakdown by account head — not read here.
//	Header row   : Symbol | ISIN | Quantity | Buy Value | Sell Value |
//	                 Realized P&L | Realized P&L Pct. | Previous Closing
//	                 Price | Open Quantity | Open Quantity Type | Open Value |
//	                 Unrealized P&L | Unrealized P&L Pct.
//	Rows after   : one instrument per row through the end of the sheet.
//
// "Quantity"/"Buy Value"/"Sell Value" describe trading that happened during
// the statement period and net to zero once a position is fully closed; they
// are not what is currently held. "Open Quantity", "Previous Closing Price"
// and "Open Value" are — that triple is a demat holdings snapshot, recorded
// through ledger.RecordHoldingSnapshot, so only
// rows with Open Quantity > 0 turn into anything: a fully closed position
// (bought and sold within the period, or held from before it and untouched
// since) has nothing left to report as a holding.
package parsers

import (
	"fmt"
	"regexp"
	"strings"

	"wollow/backend/internal/money/models"
)

var pnlTitleRe = regexp.MustCompile(`P&L Statement for (.+?) from (\d{4}-\d{2}-\d{2}) to (\d{4}-\d{2}-\d{2})`)

// pnlSheetKind maps a Console sheet tab name to the account_type-style kind
// its holdings should be filed under.
var pnlSheetKind = map[string]string{
	"equity":       "stock",
	"mutual funds": "mutual_fund",
}

// IsZerodhaPnLStatement reports whether a workbook is a Zerodha Console P&L
// export, so one upload endpoint can route it alongside HDFC's own formats.
func IsZerodhaPnLStatement(path string) bool {
	sheets, err := openXLSXSheets(path)
	if err != nil {
		return false
	}
	for name, sheet := range sheets {
		if _, ok := pnlSheetKind[strings.ToLower(strings.TrimSpace(name))]; !ok {
			continue
		}
		limit := min(sheet.maxRow(), 15)
		for r := 0; r <= limit; r++ {
			for _, cell := range sheet.row(r) {
				if pnlTitleRe.MatchString(cell) {
					return true
				}
			}
		}
	}
	return false
}

// ParseZerodhaPnLStatement reads a Console P&L export into the holdings it
// currently reports open positions for.
func ParseZerodhaPnLStatement(path string) (*models.ParsedZerodhaPnL, error) {
	sheets, err := openXLSXSheets(path)
	if err != nil {
		return nil, err
	}

	var sheetName, kind string
	var sheet *xlsxSheet
	for name, s := range sheets {
		k, ok := pnlSheetKind[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			continue
		}
		sheetName, kind, sheet = name, k, s
		break
	}
	if sheet == nil {
		return nil, fmt.Errorf("no Equity or Mutual Funds sheet found in workbook")
	}

	result := &models.ParsedZerodhaPnL{Kind: kind}

	// Client ID, the statement period and the Symbol/ISIN table header all
	// live above the data rows; one pass finds all three, since the header is
	// what ends the search.
	headerRow, symbolCol := -1, -1
	for r := 0; r <= sheet.maxRow() && headerRow == -1; r++ {
		row := sheet.row(r)
		for c, cell := range row {
			trimmed := strings.TrimSpace(cell)
			if strings.EqualFold(trimmed, "Symbol") && c+1 < len(row) && strings.EqualFold(strings.TrimSpace(row[c+1]), "ISIN") {
				headerRow, symbolCol = r, c
				break
			}
			if strings.EqualFold(trimmed, "Client ID") && c+1 < len(row) {
				result.ClientID = strings.TrimSpace(row[c+1])
			}
			if m := pnlTitleRe.FindStringSubmatch(cell); m != nil {
				result.PeriodFrom, result.PeriodTo = m[2], m[3]
			}
		}
	}
	if headerRow == -1 {
		return nil, fmt.Errorf("could not locate the Symbol/ISIN table header in %q", sheetName)
	}

	// Column positions are fixed relative to Symbol, per the layout above.
	isinCol := symbolCol + 1
	realizedPLCol := symbolCol + 5
	prevCloseCol := symbolCol + 7
	openQtyCol := symbolCol + 8
	openValueCol := symbolCol + 10
	unrealizedPLCol := symbolCol + 11

	for r := headerRow + 1; r <= sheet.maxRow(); r++ {
		row := sheet.row(r)
		if len(row) <= isinCol {
			continue
		}
		symbol := strings.TrimSpace(row[symbolCol])
		isin := strings.TrimSpace(row[isinCol])
		if symbol == "" || isin == "" {
			continue
		}
		openQty := parseAmount(cellAt(row, openQtyCol))
		if openQty <= 0 {
			continue // fully closed (or never open) — nothing currently held
		}
		result.Holdings = append(result.Holdings, models.ParsedZerodhaHolding{
			Symbol:       symbol,
			ISIN:         isin,
			Kind:         kind,
			Units:        openQty,
			Price:        parseAmount(cellAt(row, prevCloseCol)),
			Value:        parseAmount(cellAt(row, openValueCol)),
			RealizedPL:   parseAmount(cellAt(row, realizedPLCol)),
			UnrealizedPL: parseAmount(cellAt(row, unrealizedPLCol)),
		})
	}
	if len(result.Holdings) == 0 {
		return nil, fmt.Errorf("no open positions found in the %s P&L statement", sheetName)
	}
	return result, nil
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}
