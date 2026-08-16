package moneyapi

import (
	"net/http"

	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/platform/httpx"
)

type institutionItem struct {
	// Issuer is what gets stored in finance_accounts.bank. Alerts attach by
	// matching against it, so the client must send this value rather than a
	// prettier one the user typed.
	Issuer      string `json:"issuer"`
	Name        string `json:"name"`
	DefaultType string `json:"defaultType"`
}

// handleListInstitutions serves the senders Money knows how to attribute mail
// to, so the add-account form can offer them instead of asking the user to
// guess the exact spelling the parsers use.
func (s *Server) handleListInstitutions(w http.ResponseWriter, r *http.Request) {
	known := emailparse.KnownInstitutions()
	out := make([]institutionItem, 0, len(known))
	for _, inst := range known {
		out = append(out, institutionItem{
			Issuer:      inst.Issuer,
			Name:        inst.Name,
			DefaultType: string(inst.DefaultKind),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
