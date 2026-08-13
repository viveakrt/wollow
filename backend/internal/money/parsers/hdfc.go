// Package parser reads bank statement files exported from net-banking portals
// and converts them into a normalized ParsedStatement.
//
// HDFC savings/current account statements are exported as real BIFF8 (.xls)
// OLE2 compound files (not HTML-in-xls, despite that being common for other
// banks). Layout, reverse engineered from real exports:
//
//	Row 0        : "HDFC BANK Ltd. ... Page No.: N ... Statement of accounts"
//	Rows 4-17    : metadata block. Two layouts:
//	                 - col4 cells hold "Label :value" (no space after colon),
//	                   sometimes two "Label :value" pairs in one cell separated
//	                   by runs of spaces (e.g. "OD Limit :0   Currency :INR").
//	                 - col0 cells hold "Label  :  value" (space-colon-space)
//	                   for JOINT HOLDERS / Nomination / Statement From..To.
//	Row ~20      : transaction table header:
//	                 Date | Narration | Chq./Ref.No. | Value Dt |
//	                 Withdrawal Amt. | Deposit Amt. | Closing Balance
//	Rows 21..N   : transaction rows (row right after header may be a masking
//	                 row of "*" and should be skipped).
//	"STATEMENT SUMMARY :-" section near the end: one row of column labels
//	(Opening Balance / Debits / Credits / Closing Bal, then Dr Count / Cr
//	Count on the row below that) followed immediately by a row holding the
//	matching values in the same column positions.
//
// Dates are DD/MM/YY. Amounts are plain decimals, no thousands separators.
package parsers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shakinm/xlsReader/xls"
	"github.com/shakinm/xlsReader/xls/structure"
	"wollow/backend/internal/money/models"
)

var metaLabelRe = regexp.MustCompile(`^\s*([A-Za-z0-9 ./&#-]+?)\s*:\s*(.*)$`)

// splitLabelPairs splits a cell like "OD Limit :0   Currency :INR" into
// [{OD Limit, 0}, {Currency, INR}] by finding label boundaries at runs of
// 2+ spaces that precede another "Label :" pattern.
func splitLabelPairs(cell string) map[string]string {
	out := map[string]string{}
	// Split on 2+ consecutive spaces, then each chunk should be "Label :value" or a trailing value continuation.
	chunks := regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(cell), -1)
	for _, c := range chunks {
		if m := metaLabelRe.FindStringSubmatch(c); m != nil {
			out[strings.ToUpper(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
		}
	}
	return out
}

type colGetter interface {
	GetCols() []structure.CellData
}

func cellString(row colGetter, idx int) string {
	cols := row.GetCols()
	if idx < 0 || idx >= len(cols) {
		return ""
	}
	return strings.TrimSpace(cols[idx].GetString())
}

func parseAmount(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseHDFCDate converts DD/MM/YY (as used throughout HDFC exports) to YYYY-MM-DD.
func parseHDFCDate(s string) string {
	s = strings.TrimSpace(s)
	layouts := []string{"02/01/06", "02/01/2006"}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// ParseHDFCStatement parses an HDFC savings/current account .xls export.
func ParseHDFCStatement(path string) (*models.ParsedStatement, error) {
	wb, err := xls.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xls: %w", err)
	}
	sheet, err := wb.GetSheet(0)
	if err != nil {
		return nil, fmt.Errorf("get sheet: %w", err)
	}

	numRows := sheet.GetNumberRows()
	result := &models.ParsedStatement{Bank: "HDFC"}

	meta := map[string]string{}
	headerRow := -1

	// Scan metadata block + locate the transaction header row.
	for r := 0; r < numRows; r++ {
		row, err := sheet.GetRow(r)
		if err != nil {
			continue
		}
		c0 := cellString(row, 0)
		c4 := cellString(row, 4)

		if c0 != "" {
			for k, v := range splitLabelPairs(c0) {
				meta[k] = v
			}
		}
		if c4 != "" {
			for k, v := range splitLabelPairs(c4) {
				meta[k] = v
			}
		}

		if strings.EqualFold(c0, "Date") && strings.Contains(strings.ToLower(cellString(row, 1)), "narration") {
			headerRow = r
			break
		}
	}
	if headerRow == -1 {
		return nil, fmt.Errorf("could not locate transaction header row (Date/Narration columns)")
	}

	result.AccountNumber = meta["ACCOUNT NO"]
	result.AccountBranch = meta["ACCOUNT BRANCH"]
	result.IFSC = meta["RTGS/NEFT IFSC"]

	// Transaction rows: from headerRow+1 until a blank Date cell run or the
	// "STATEMENT SUMMARY" marker is hit.
	var txns []models.ParsedTransaction
	summaryRow := -1
	for r := headerRow + 1; r < numRows; r++ {
		row, err := sheet.GetRow(r)
		if err != nil {
			continue
		}
		c0 := cellString(row, 0)
		if strings.Contains(strings.ToUpper(c0), "STATEMENT SUMMARY") {
			summaryRow = r
			break
		}
		if c0 == "" || strings.HasPrefix(c0, "*") {
			continue // masking row / blank separator
		}
		dateVal := parseHDFCDate(c0)
		if len(dateVal) != 10 || dateVal[4] != '-' {
			continue // not a real transaction row
		}

		withdrawal := parseAmount(cellString(row, 4))
		deposit := parseAmount(cellString(row, 5))
		closing := parseAmount(cellString(row, 6))
		narration := cellString(row, 1)

		txnType := "expense"
		if deposit > 0 {
			txnType = "income"
		}

		pt := models.ParsedTransaction{
			TxnDate:        dateVal,
			ValueDate:      parseHDFCDate(cellString(row, 3)),
			Narration:      narration,
			RefNo:          cellString(row, 2),
			WithdrawalAmt:  withdrawal,
			DepositAmt:     deposit,
			ClosingBalance: closing,
			Type:           txnType,
		}
		pt.Merchant, pt.PaymentMethod = extractMerchant(narration)
		pt.DedupeHash = dedupeHash(pt)
		txns = append(txns, pt)
	}
	result.Transactions = txns

	// Statement summary: label row is summaryRow+1, value row is summaryRow+2.
	if summaryRow != -1 && summaryRow+2 < numRows {
		labelRow, err1 := sheet.GetRow(summaryRow + 1)
		valueRow, err2 := sheet.GetRow(summaryRow + 2)
		if err1 == nil && err2 == nil {
			for i := 0; i < 8; i++ {
				label := strings.ToUpper(cellString(labelRow, i))
				val := cellString(valueRow, i)
				switch {
				case strings.Contains(label, "OPENING BALANCE"):
					result.OpeningBalance = parseAmount(val)
				case strings.Contains(label, "CLOSING BAL"):
					result.ClosingBalance = parseAmount(val)
				}
			}
		}
	}
	if result.ClosingBalance == 0 && len(txns) > 0 {
		result.ClosingBalance = txns[len(txns)-1].ClosingBalance
	}
	if len(txns) > 0 {
		result.StatementFrom = txns[0].TxnDate
		result.StatementTo = txns[len(txns)-1].TxnDate
	}

	return result, nil
}

// paymentMethodPrefixes maps narration prefixes to a normalized payment method label.
var paymentMethodPrefixes = []struct {
	prefix string
	method string
}{
	{"UPI-", "UPI"},
	{"NEFT CR-", "NEFT"},
	{"NEFT DR-", "NEFT"},
	{"IMPS-", "IMPS"},
	{"RTGS-", "RTGS"},
	{"POS ", "POS"},
	{"ATW-", "ATM"},
	{"ATM-", "ATM"},
	{"NWD-", "ATM"},
	{"BIL/", "BILLPAY"},
	{"ECS", "ECS"},
	{"ACH ", "ACH"},
}

// extractMerchant pulls a merchant/counterparty name and payment method out
// of an HDFC narration string. HDFC narrations are hyphen-delimited with the
// counterparty typically in the 2nd segment for UPI, 3rd for NEFT credits.
func extractMerchant(narration string) (merchant, method string) {
	upper := strings.ToUpper(narration)
	for _, pm := range paymentMethodPrefixes {
		if strings.HasPrefix(upper, pm.prefix) {
			method = pm.method
			break
		}
	}
	if method == "" {
		return "", ""
	}

	parts := strings.Split(narration, "-")
	switch method {
	case "UPI":
		if len(parts) >= 2 {
			merchant = strings.TrimSpace(parts[1])
		}
	case "NEFT":
		if strings.HasPrefix(upper, "NEFT CR-") && len(parts) >= 3 {
			merchant = strings.TrimSpace(parts[2])
		} else if len(parts) >= 2 {
			merchant = strings.TrimSpace(parts[1])
		}
	case "POS":
		fields := strings.Fields(narration)
		if len(fields) > 0 {
			merchant = strings.TrimSpace(fields[len(fields)-1])
		}
	default:
		if len(parts) >= 2 {
			merchant = strings.TrimSpace(parts[1])
		}
	}
	return merchant, method
}

func dedupeHash(t models.ParsedTransaction) string {
	h := sha256.New()
	h.Write([]byte(t.TxnDate))
	h.Write([]byte(t.RefNo))
	h.Write([]byte(fmt.Sprintf("%.2f|%.2f", t.WithdrawalAmt, t.DepositAmt)))
	h.Write([]byte(t.Narration))
	return hex.EncodeToString(h.Sum(nil))
}
