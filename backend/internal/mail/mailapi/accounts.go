package mailapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/mail"
)

type accountResponse struct {
	ID           int64  `json:"id"`
	Label        string `json:"label"`
	ProviderType string `json:"providerType"`
	ImapHost     string `json:"imapHost"`
	ImapPort     int    `json:"imapPort"`
	SMTPHost     string `json:"smtpHost"`
	SMTPPort     int    `json:"smtpPort"`
	Username     string `json:"username"`
	UseTLS       bool   `json:"useTls"`
	CreatedAt    string `json:"createdAt"`
}

type createAccountRequest struct {
	Label    string `json:"label"`
	ImapHost string `json:"imapHost"`
	ImapPort int    `json:"imapPort"`
	SMTPHost string `json:"smtpHost"`
	SMTPPort int    `json:"smtpPort"`
	Username string `json:"username"`
	Password string `json:"password"`
	UseTLS   bool   `json:"useTls"`
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT id, label, provider_type, imap_host, imap_port, smtp_host, smtp_port, username, use_tls, created_at FROM mail_accounts ORDER BY id`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}
	defer rows.Close()

	accounts := []accountResponse{}
	for rows.Next() {
		var a accountResponse
		var useTLS int
		if err := rows.Scan(&a.ID, &a.Label, &a.ProviderType, &a.ImapHost, &a.ImapPort, &a.SMTPHost, &a.SMTPPort, &a.Username, &useTLS, &a.CreatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "failed to read account")
			return
		}
		a.UseTLS = useTLS != 0
		accounts = append(accounts, a)
	}
	httpx.WriteJSON(w, http.StatusOK, accounts)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Label == "" || req.ImapHost == "" || req.ImapPort == 0 || req.Username == "" || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "label, imapHost, imapPort, username, and password are required")
		return
	}

	// Verify the credentials actually work before persisting them.
	provider, err := s.providerFactory(mail.AccountCredentials{
		Host:     req.ImapHost,
		Port:     req.ImapPort,
		Username: req.Username,
		Password: req.Password,
		UseTLS:   req.UseTLS,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "could not connect to IMAP server: "+err.Error())
		return
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	encryptedPassword, err := s.Box.Encrypt(req.Password)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to encrypt credentials")
		return
	}

	useTLS := 0
	if req.UseTLS {
		useTLS = 1
	}

	result, err := s.DB.Exec(
		`INSERT INTO mail_accounts (label, provider_type, imap_host, imap_port, smtp_host, smtp_port, username, encrypted_password, use_tls) VALUES (?, 'imap', ?, ?, ?, ?, ?, ?, ?)`,
		req.Label, req.ImapHost, req.ImapPort, req.SMTPHost, req.SMTPPort, req.Username, encryptedPassword, useTLS,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to save account")
		return
	}
	id, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM mail_accounts WHERE id = ?`, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// loadAccountCredentials fetches and decrypts the credentials for an account id.
func (s *Server) loadAccountCredentials(id int64) (mail.AccountCredentials, error) {
	var host, username, encryptedPassword string
	var port, useTLS int
	err := s.DB.QueryRow(
		`SELECT imap_host, imap_port, username, encrypted_password, use_tls FROM mail_accounts WHERE id = ?`, id,
	).Scan(&host, &port, &username, &encryptedPassword, &useTLS)
	if err != nil {
		return mail.AccountCredentials{}, err
	}
	password, err := s.Box.Decrypt(encryptedPassword)
	if err != nil {
		return mail.AccountCredentials{}, err
	}
	return mail.AccountCredentials{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		UseTLS:   useTLS != 0,
	}, nil
}
