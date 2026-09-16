package parserules

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Queryer is what the store needs from a database handle; *sql.DB and *sql.Tx
// both satisfy it.
type Queryer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

const ruleColumns = `
	id, name, kind, enabled, priority, issuer, sender_domain, sender_email,
	subject_contains, body_contains, COALESCE(account_id, 0), account_type,
	attributes, fields, COALESCE(sample_mail_account_id, 0), sample_uid, sample_folder,
	sample_rfc_message_id, sample_subject, sample_from, sample_date, sample_text,
	match_count, last_matched_at, created_at, updated_at`

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanRule(row scanner) (*Rule, error) {
	var (
		r                  Rule
		enabled            int
		attributes, fields string
	)
	if err := row.Scan(&r.ID, &r.Name, &r.Kind, &enabled, &r.Priority, &r.Issuer, &r.SenderDomain,
		&r.SenderEmail, &r.SubjectContains, &r.BodyContains, &r.AccountID, &r.AccountType,
		&attributes, &fields, &r.Sample.MailAccountID, &r.Sample.UID, &r.Sample.Folder,
		&r.Sample.RFCMessageID, &r.Sample.Subject, &r.Sample.From, &r.Sample.Date, &r.Sample.Text,
		&r.MatchCount, &r.LastMatchedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	if attributes != "" {
		if err := json.Unmarshal([]byte(attributes), &r.Attributes); err != nil {
			return nil, fmt.Errorf("rule %d: attributes: %w", r.ID, err)
		}
	}
	r.Fields = []Field{}
	if fields != "" {
		if err := json.Unmarshal([]byte(fields), &r.Fields); err != nil {
			return nil, fmt.Errorf("rule %d: fields: %w", r.ID, err)
		}
	}
	return &r, nil
}

func loadWhere(db Queryer, where string, args ...interface{}) ([]*Rule, error) {
	rows, err := db.Query(`SELECT `+ruleColumns+` FROM email_parser_rules `+where+
		` ORDER BY priority DESC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadAll returns every rule, enabled or not, in the order ingest tries them.
func LoadAll(db Queryer) ([]*Rule, error) { return loadWhere(db, "") }

// LoadEnabled returns the rules ingest applies, highest priority first.
func LoadEnabled(db Queryer) ([]*Rule, error) { return loadWhere(db, "WHERE enabled = 1") }

func Get(db Queryer, id int64) (*Rule, error) {
	return scanRule(db.QueryRow(`SELECT `+ruleColumns+` FROM email_parser_rules WHERE id = ?`, id))
}

func encode(r *Rule) (attributes, fields string, err error) {
	a, err := json.Marshal(r.Attributes)
	if err != nil {
		return "", "", err
	}
	if r.Fields == nil {
		r.Fields = []Field{}
	}
	f, err := json.Marshal(r.Fields)
	if err != nil {
		return "", "", err
	}
	return string(a), string(f), nil
}

func normalizeRule(r *Rule) {
	r.Name = strings.TrimSpace(r.Name)
	r.Issuer = strings.TrimSpace(r.Issuer)
	r.SenderDomain = strings.ToLower(strings.TrimSpace(r.SenderDomain))
	r.SenderEmail = strings.ToLower(strings.TrimSpace(r.SenderEmail))
	r.SubjectContains = strings.TrimSpace(r.SubjectContains)
	r.BodyContains = strings.TrimSpace(r.BodyContains)
	if r.AccountType == "" {
		r.AccountType = "bank"
	}
	if r.Sample.Folder == "" {
		r.Sample.Folder = "INBOX"
	}
}

// Insert stores a new rule and returns its id.
func Insert(db Queryer, r *Rule) (int64, error) {
	normalizeRule(r)
	attributes, fields, err := encode(r)
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`
		INSERT INTO email_parser_rules
			(name, kind, enabled, priority, issuer, sender_domain, sender_email,
			 subject_contains, body_contains, account_id, account_type, attributes, fields,
			 sample_mail_account_id, sample_uid, sample_folder, sample_rfc_message_id,
			 sample_subject, sample_from, sample_date, sample_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, string(r.Kind), boolToInt(r.Enabled), r.Priority, r.Issuer, r.SenderDomain, r.SenderEmail,
		r.SubjectContains, r.BodyContains, nullIfZero(r.AccountID), r.AccountType, attributes, fields,
		nullIfZero(r.Sample.MailAccountID), r.Sample.UID, r.Sample.Folder, r.Sample.RFCMessageID,
		r.Sample.Subject, r.Sample.From, r.Sample.Date, r.Sample.Text)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	r.ID = id
	return id, nil
}

// Update rewrites a rule in place. The match counters are left alone.
func Update(db Queryer, r *Rule) error {
	normalizeRule(r)
	attributes, fields, err := encode(r)
	if err != nil {
		return err
	}
	res, err := db.Exec(`
		UPDATE email_parser_rules SET
			name = ?, kind = ?, enabled = ?, priority = ?, issuer = ?, sender_domain = ?,
			sender_email = ?, subject_contains = ?, body_contains = ?, account_id = ?,
			account_type = ?, attributes = ?, fields = ?, sample_mail_account_id = ?,
			sample_uid = ?, sample_folder = ?, sample_rfc_message_id = ?, sample_subject = ?,
			sample_from = ?, sample_date = ?, sample_text = ?, updated_at = datetime('now')
		WHERE id = ?`,
		r.Name, string(r.Kind), boolToInt(r.Enabled), r.Priority, r.Issuer, r.SenderDomain,
		r.SenderEmail, r.SubjectContains, r.BodyContains, nullIfZero(r.AccountID),
		r.AccountType, attributes, fields, nullIfZero(r.Sample.MailAccountID),
		r.Sample.UID, r.Sample.Folder, r.Sample.RFCMessageID, r.Sample.Subject,
		r.Sample.From, r.Sample.Date, r.Sample.Text, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func Delete(db Queryer, id int64) error {
	res, err := db.Exec(`DELETE FROM email_parser_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecordMatch counts one message read by the rule.
func RecordMatch(db Queryer, id int64) {
	db.Exec(`UPDATE email_parser_rules
		SET match_count = match_count + 1, last_matched_at = datetime('now') WHERE id = ?`, id)
}

// CompileAll prepares every rule that compiles, reporting the ones that don't
// so a broken rule disables itself rather than the whole pass.
func CompileAll(rules []*Rule) (compiled []*Compiled, errs []error) {
	for _, r := range rules {
		c, err := Compile(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %d (%s): %w", r.ID, r.Name, err))
			continue
		}
		compiled = append(compiled, c)
	}
	return compiled, errs
}

// Senders lists the sender domains and exact addresses the given rules
// cover, for candidate selection.
func Senders(rules []*Rule) (domains, emails []string) {
	seenD, seenE := map[string]bool{}, map[string]bool{}
	for _, r := range rules {
		if d := strings.ToLower(strings.TrimSpace(r.SenderDomain)); d != "" && !seenD[d] {
			seenD[d] = true
			domains = append(domains, d)
		}
		if e := strings.ToLower(strings.TrimSpace(r.SenderEmail)); e != "" && !seenE[e] {
			seenE[e] = true
			emails = append(emails, e)
		}
	}
	return domains, emails
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfZero(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}
