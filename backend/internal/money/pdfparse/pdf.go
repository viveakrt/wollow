// Package pdfparser extracts itemized transactions from credit card
// statement PDFs (ICICI, BOBCARD, HDFC Diners, ...), which are usually
// password-protected with an issuer-specific fixed formula.
package pdfparse

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// decrypt removes password protection, returning a plain (unencrypted) PDF
// as bytes. Indian bank/card statement PDFs are commonly AES-256 encrypted,
// which ledongthuc/pdf can't open directly — pdfcpu handles the wider range
// of encryption schemes, so it does the unlocking and ledongthuc/pdf (below)
// does the text extraction on the now-plain copy.
func decrypt(content []byte, password string) ([]byte, error) {
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password

	var out bytes.Buffer
	if err := api.Decrypt(bytes.NewReader(content), &out, conf); err != nil {
		return nil, fmt.Errorf("incorrect password or unsupported encryption: %w", err)
	}
	return out.Bytes(), nil
}

// ExtractText opens a PDF (optionally password-protected) and returns its
// full plain text content, page breaks joined by "\f".
func ExtractText(content []byte, password string) (string, error) {
	if password != "" {
		plain, err := decrypt(content, password)
		if err != nil {
			return "", err
		}
		content = plain
	}

	reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}

	var sb strings.Builder
	numPages := reader.NumPage()
	for i := 1; i <= numPages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue // skip unreadable pages rather than failing the whole statement
		}
		sb.WriteString(text)
		sb.WriteString("\f")
	}
	return sb.String(), nil
}
