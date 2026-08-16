package moneyapi

import (
	"net/http"

	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/money/pdfparse"
	"wollow/backend/internal/platform/httpx"
)

// PDFPasswordLookup adapts the stored, encrypted pdf_passwords table to the
// plain function ingest needs — ingest has no reason to know how passwords
// are encrypted at rest, only whether one is available for a given issuer.
// Exported so cmd/server can wire it into the AfterSync hook, which runs
// outside this package.
func (s *Server) PDFPasswordLookup() ingest.PDFPasswordLookup {
	return func(issuer string) (string, bool) {
		var encrypted string
		if err := s.DB.QueryRow(
			`SELECT encrypted_password FROM pdf_passwords WHERE issuer = ?`, issuer,
		).Scan(&encrypted); err != nil {
			return "", false
		}
		password, err := s.Box.Decrypt(encrypted)
		if err != nil {
			return "", false
		}
		return password, true
	}
}

type pdfPassword struct {
	ID       int64  `json:"id"`
	Issuer   string `json:"issuer"`
	HasValue bool   `json:"hasValue"`
}

func (s *Server) handleListPDFPasswords(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT id, issuer FROM pdf_passwords ORDER BY issuer`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	out := []pdfPassword{}
	for rows.Next() {
		var p pdfPassword
		if err := rows.Scan(&p.ID, &p.Issuer); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		p.HasValue = true
		out = append(out, p)
	}
	httpx.WriteJSON(w, 200, out)
}

type setPDFPasswordRequest struct {
	Issuer   string `json:"issuer"`
	Password string `json:"password"`
}

func (s *Server) handleSetPDFPassword(w http.ResponseWriter, r *http.Request) {
	var req setPDFPasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if req.Issuer == "" || req.Password == "" {
		httpx.WriteError(w, 400, "issuer and password are required")
		return
	}
	// Issuer PDF passwords are usually a fixed formula (name+DOB) rather than a
	// secret, but they are personal data and sit in the same file as everything
	// else, so they get the same AES-GCM treatment as mailbox passwords.
	encrypted, err := s.Box.Encrypt(req.Password)
	if err != nil {
		httpx.WriteError(w, 500, "failed to encrypt password")
		return
	}
	_, err = s.DB.Exec(`
		INSERT INTO pdf_passwords (issuer, encrypted_password) VALUES (?, ?)
		ON CONFLICT(issuer) DO UPDATE SET encrypted_password = excluded.encrypted_password`,
		req.Issuer, encrypted)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"saved": true})
}

// handleParsePendingBillPDFs attempts to extract text from every
// not-yet-parsed PDF attachment, using the stored password for that bill's
// issuer. Bills whose issuer has no stored password, or whose password is
// wrong, are left as pending with the error recorded — safe to retry after
// the user adds/fixes the password in Settings.
func (s *Server) handleParsePendingBillPDFs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT pa.id, pa.content, b.issuer
		FROM pdf_attachments pa
		JOIN bills b ON b.id = pa.bill_id
		WHERE pa.parsed = 0`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	type job struct {
		attachmentID int64
		content      []byte
		issuer       string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.attachmentID, &j.content, &j.issuer); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	parsed, failed := 0, 0
	for _, j := range jobs {
		var encrypted string
		err := s.DB.QueryRow(`SELECT encrypted_password FROM pdf_passwords WHERE issuer = ?`, j.issuer).Scan(&encrypted)
		if err != nil {
			s.DB.Exec(`UPDATE pdf_attachments SET parse_error = ? WHERE id = ?`, "no password configured for "+j.issuer, j.attachmentID)
			failed++
			continue
		}
		password, err := s.Box.Decrypt(encrypted)
		if err != nil {
			s.DB.Exec(`UPDATE pdf_attachments SET parse_error = ? WHERE id = ?`, "could not decrypt stored password", j.attachmentID)
			failed++
			continue
		}

		text, err := pdfparse.ExtractText(j.content, password)
		if err != nil {
			s.DB.Exec(`UPDATE pdf_attachments SET parse_error = ? WHERE id = ?`, err.Error(), j.attachmentID)
			failed++
			continue
		}

		// Itemized transaction extraction from `text` is issuer-specific and
		// not yet implemented (see internal/pdfparser statement parsers,
		// pending real sample layouts). Mark as parsed so it isn't retried
		// forever; extraction can be added without touching the sync/decrypt
		// path once those parsers exist.
		_ = text
		s.DB.Exec(`UPDATE pdf_attachments SET parsed = 1, parse_error = '' WHERE id = ?`, j.attachmentID)
		parsed++
	}

	httpx.WriteJSON(w, 200, map[string]int{"parsed": parsed, "failed": failed})
}
