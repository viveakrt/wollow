package moneyapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/money/parserules"
	"wollow/backend/internal/platform/httpx"
)

// parserRuleInput is a rule as the editor submits it: the settings, and
// either the spans marked in the sample (from which the server derives the
// fields) or fields already derived by an earlier round trip.
type parserRuleInput struct {
	parserules.Rule
	EnabledOpt *bool             `json:"enabled"`
	Spans      []parserules.Span `json:"spans"`
}

// buildRule validates the submission and turns marked spans into anchored
// fields. The result must read its own sample, which catches a rule that
// cannot work before it is saved.
func buildRule(in *parserRuleInput) (*parserules.Rule, error) {
	r := in.Rule
	r.Enabled = in.EnabledOpt == nil || *in.EnabledOpt
	if strings.TrimSpace(r.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if !parserules.ValidKind(r.Kind) {
		return nil, fmt.Errorf("unknown rule kind %q", r.Kind)
	}
	r.Sample.Text = parserules.NormalizeText(r.Sample.Text)
	if len(in.Spans) > 0 {
		if r.Sample.Text == "" {
			return nil, fmt.Errorf("the sample text is required to derive the values")
		}
		fields, err := parserules.Derive(r.Sample.Text, in.Spans)
		if err != nil {
			return nil, err
		}
		r.Fields = fields
	}
	compiled, err := parserules.Compile(&r)
	if err != nil {
		return nil, err
	}
	if r.Sample.Text != "" {
		if _, err := compiled.Apply(r.Sample.Text); err != nil {
			return nil, fmt.Errorf("the rule does not read its own sample: %v", err)
		}
	}
	return &r, nil
}

type parserRuleSummary struct {
	*parserules.Rule
	HasSample bool `json:"hasSample"`
}

func (s *Server) handleListParserRules(w http.ResponseWriter, r *http.Request) {
	rules, err := parserules.LoadAll(s.DB)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	out := make([]parserRuleSummary, 0, len(rules))
	for _, rule := range rules {
		hasSample := rule.Sample.Text != ""
		rule.Sample.Text = "" // the list is a table; the text is only needed by the editor
		out = append(out, parserRuleSummary{Rule: rule, HasSample: hasSample})
	}
	httpx.WriteJSON(w, 200, out)
}

func (s *Server) handleGetParserRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	rule, err := parserules.Get(s.DB, id)
	if err != nil {
		httpx.WriteError(w, 404, "rule not found")
		return
	}
	httpx.WriteJSON(w, 200, rule)
}

func (s *Server) handleCreateParserRule(w http.ResponseWriter, r *http.Request) {
	var in parserRuleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	rule, err := buildRule(&in)
	if err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	if _, err := parserules.Insert(s.DB, rule); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	saved, err := parserules.Get(s.DB, rule.ID)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 201, saved)
}

func (s *Server) handleUpdateParserRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var in parserRuleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	// The list view carries rules without their sample text, so a toggle from
	// there must not erase the text the editor needs later.
	if strings.TrimSpace(in.Sample.Text) == "" && len(in.Spans) == 0 {
		existing, err := parserules.Get(s.DB, id)
		if err != nil {
			httpx.WriteError(w, 404, "rule not found")
			return
		}
		in.Sample = existing.Sample
		if len(in.Fields) == 0 {
			in.Fields = existing.Fields
		}
	}
	rule, err := buildRule(&in)
	if err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	rule.ID = id
	if err := parserules.Update(s.DB, rule); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, 404, "rule not found")
			return
		}
		httpx.WriteError(w, 500, err.Error())
		return
	}
	saved, err := parserules.Get(s.DB, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, saved)
}

func (s *Server) handleDeleteParserRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	if err := parserules.Delete(s.DB, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, 404, "rule not found")
			return
		}
		httpx.WriteError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// handleParserRuleFields serves the catalogue of values each kind of rule can
// extract, so the editor and the server never disagree about field names.
func (s *Server) handleParserRuleFields(w http.ResponseWriter, r *http.Request) {
	out := map[parserules.Kind][]parserules.FieldSpec{}
	for _, k := range []parserules.Kind{parserules.KindTransaction, parserules.KindBill, parserules.KindBalance, parserules.KindTrade} {
		out[k] = parserules.FieldsForKind(k)
	}
	httpx.WriteJSON(w, 200, out)
}

// parserSample is a message as the rule editor needs it: the exact text
// ingest will apply the rule to, plus where the message lives.
type parserSample struct {
	parserules.Sample
	FromDomain string `json:"fromDomain"`
	HasPDF     bool   `json:"hasPdf"`
	// Issuer and DefaultKind are the registry's guess for the sender, to
	// prefill the rule; empty for a sender the registry doesn't know.
	Issuer      string `json:"issuer"`
	DefaultKind string `json:"defaultKind"`
}

var errMessageGone = errors.New("message not found in the mailbox")

// fetchSample pulls one message over the mailbox connection and normalizes
// it the way ingest would. The Mail API's own body text is deliberately not
// used: the rule must be defined on the text it will be applied to.
func (s *Server) fetchSample(ctx context.Context, mailAccountID int64, folder string, uid uint32) (*parserSample, error) {
	var sample *parserSample
	err := s.withMailSession(ctx, mailAccountID, func(fetcher ingest.RawFetcher) error {
		raws, err := fetcher.FetchRaw(ctx, folder, []uint32{uid})
		if err != nil {
			return err
		}
		if len(raws) == 0 {
			return errMessageGone
		}
		parsed, err := emailparse.ParseEML(raws[0].Raw)
		if err != nil {
			return fmt.Errorf("decoding message: %w", err)
		}
		sample = &parserSample{
			Sample: parserules.Sample{
				MailAccountID: mailAccountID,
				UID:           uid,
				Folder:        folder,
				RFCMessageID:  parsed.MessageID,
				Subject:       parsed.Subject,
				From:          parsed.From,
				Date:          parsed.Date,
				Text:          parserules.SampleText(parsed.Subject, parsed.TextBody),
			},
			HasPDF: parsed.HasPDF,
		}
		if at := strings.LastIndex(parsed.From, "@"); at != -1 {
			sample.FromDomain = strings.ToLower(parsed.From[at+1:])
		}
		if inst := emailparse.InstitutionForSender(parsed.From); inst != nil {
			sample.Issuer = inst.Issuer
			sample.DefaultKind = string(inst.DefaultKind)
		}
		return nil
	})
	return sample, err
}

func (s *Server) handleParserRuleSample(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mailAccountID, err := strconv.ParseInt(q.Get("mailAccountId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "mailAccountId is required")
		return
	}
	uid, err := strconv.ParseUint(q.Get("uid"), 10, 32)
	if err != nil || uid == 0 {
		httpx.WriteError(w, 400, "uid is required")
		return
	}
	folder := q.Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	sample, err := s.fetchSample(r.Context(), mailAccountID, folder, uint32(uid))
	switch {
	case errors.Is(err, errMessageGone):
		httpx.WriteError(w, 404, err.Error())
		return
	case err != nil && s.mailSession == nil:
		httpx.WriteError(w, 500, err.Error())
		return
	case err != nil:
		httpx.WriteError(w, 502, "could not fetch the message: "+err.Error())
		return
	}
	httpx.WriteJSON(w, 200, sample)
}

type testParserRuleRequest struct {
	Rule          parserRuleInput `json:"rule"`
	MailAccountID int64           `json:"mailAccountId"`
	Limit         int             `json:"limit"`
}

type testParserRuleResult struct {
	UID     uint32            `json:"uid"`
	Subject string            `json:"subject"`
	Date    string            `json:"date"`
	Matched bool              `json:"matched"`
	Values  map[string]string `json:"values,omitempty"`
	Reason  string            `json:"reason,omitempty"`
}

type testParserRuleResponse struct {
	Fields  []parserules.Field     `json:"fields"`
	Results []testParserRuleResult `json:"results"`
}

// handleTestParserRule dry-runs a draft rule against the newest messages from
// its sender, so the user sees what it would read before saving. Nothing is
// written.
func (s *Server) handleTestParserRule(w http.ResponseWriter, r *http.Request) {
	var req testParserRuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	rule, err := buildRule(&req.Rule)
	if err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	compiled, err := parserules.Compile(rule)
	if err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	type indexed struct {
		uid     uint32
		subject string
		date    string
	}
	rows, err := s.DB.Query(`
		SELECT uid, subject, date FROM messages
		WHERE account_id = ? AND folder = ?
		  AND ((? != '' AND LOWER(from_email) = ?)
		    OR (? != '' AND (from_domain = ? OR from_domain LIKE ?)))
		ORDER BY date DESC, uid DESC
		LIMIT ?`,
		req.MailAccountID, rule.Sample.Folder,
		rule.SenderEmail, rule.SenderEmail,
		rule.SenderDomain, rule.SenderDomain, "%."+rule.SenderDomain, limit)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	var messages []indexed
	for rows.Next() {
		var m indexed
		if err := rows.Scan(&m.uid, &m.subject, &m.date); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		messages = append(messages, m)
	}
	rows.Close()

	resp := testParserRuleResponse{Fields: rule.Fields, Results: []testParserRuleResult{}}
	if len(messages) == 0 {
		httpx.WriteJSON(w, 200, resp)
		return
	}

	uids := make([]uint32, 0, len(messages))
	for _, m := range messages {
		uids = append(uids, m.uid)
	}
	byUID := map[uint32][]byte{}
	err = s.withMailSession(r.Context(), req.MailAccountID, func(fetcher ingest.RawFetcher) error {
		raws, err := fetcher.FetchRaw(r.Context(), rule.Sample.Folder, uids)
		if err != nil {
			return err
		}
		for _, raw := range raws {
			byUID[raw.UID] = raw.Raw
		}
		return nil
	})
	if err != nil {
		httpx.WriteError(w, 502, "could not fetch messages: "+err.Error())
		return
	}

	for _, m := range messages {
		result := testParserRuleResult{UID: m.uid, Subject: m.subject, Date: m.date}
		raw, ok := byUID[m.uid]
		if !ok {
			result.Reason = "message no longer in the mailbox"
			resp.Results = append(resp.Results, result)
			continue
		}
		parsed, err := emailparse.ParseEML(raw)
		if err != nil {
			result.Reason = "could not decode the message"
			resp.Results = append(resp.Results, result)
			continue
		}
		text := parserules.SampleText(parsed.Subject, parsed.TextBody)
		fromDomain := ""
		if at := strings.LastIndex(parsed.From, "@"); at != -1 {
			fromDomain = parsed.From[at+1:]
		}
		if !compiled.Matches(parsed.From, fromDomain, parsed.Subject, text) {
			result.Reason = "sender or subject does not match the rule"
			resp.Results = append(resp.Results, result)
			continue
		}
		values, err := compiled.Apply(text)
		if err != nil {
			result.Reason = err.Error()
			resp.Results = append(resp.Results, result)
			continue
		}
		result.Matched = true
		result.Values = values
		resp.Results = append(resp.Results, result)
	}
	httpx.WriteJSON(w, 200, resp)
}

// handleRescanParserRule applies a rule to the mail that arrived before it
// existed: every message from its sender that nothing could read is released
// and every mailbox re-ingested.
func (s *Server) handleRescanParserRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	rule, err := parserules.Get(s.DB, id)
	if err != nil {
		httpx.WriteError(w, 404, "rule not found")
		return
	}

	rows, err := s.DB.Query(`SELECT id FROM mail_accounts ORDER BY id`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	var mailboxes []int64
	for rows.Next() {
		var mailboxID int64
		if err := rows.Scan(&mailboxID); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		mailboxes = append(mailboxes, mailboxID)
	}
	rows.Close()

	total := rescanResult{Result: &ingest.Result{}}
	for _, mailboxID := range mailboxes {
		cleared, err := ingest.ClearForRule(s.DB, mailboxID, rule)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		total.Cleared += cleared

		var result *ingest.Result
		err = s.withMailSession(r.Context(), mailboxID, func(fetcher ingest.RawFetcher) error {
			var runErr error
			result, runErr = ingest.Run(r.Context(), s.DB, fetcher, mailboxID, "INBOX")
			return runErr
		})
		if err != nil {
			httpx.WriteError(w, 502, "rescan failed: "+err.Error())
			return
		}
		addResult(total.Result, result)
	}
	httpx.WriteJSON(w, 200, total)
}

func addResult(dst, src *ingest.Result) {
	dst.Scanned += src.Scanned
	dst.Transactions += src.Transactions
	dst.Bills += src.Bills
	dst.Balances += src.Balances
	dst.Trades += src.Trades
	dst.PendingAccount += src.PendingAccount
	dst.Unrecognized += src.Unrecognized
	dst.Duplicates += src.Duplicates
	dst.Failed += src.Failed
}
