package parserules

import (
	"fmt"
	"strings"

	"wollow/backend/internal/money/emailparse"
)

// Compiled is a rule ready to be applied: its field anchors as regular
// expressions, built once per ingest pass rather than per message.
type Compiled struct {
	Rule   *Rule
	fields []compiledField
}

// Compile validates a rule and prepares it for matching.
func Compile(r *Rule) (*Compiled, error) {
	if !ValidKind(r.Kind) {
		return nil, fmt.Errorf("unknown rule kind %q", r.Kind)
	}
	if r.SenderDomain == "" && r.SenderEmail == "" {
		return nil, fmt.Errorf("a rule needs a sender domain or address")
	}
	if len(r.Fields) == 0 {
		return nil, fmt.Errorf("a rule needs at least one value to extract")
	}
	if len(r.Fields) > maxFields {
		return nil, fmt.Errorf("a rule can extract at most %d values", maxFields)
	}
	c := &Compiled{Rule: r}
	for _, f := range r.Fields {
		if _, ok := kindForName(f.Name); !ok {
			return nil, fmt.Errorf("unknown field %q", f.Name)
		}
		re, err := compileField(f)
		if err != nil {
			return nil, err
		}
		c.fields = append(c.fields, compiledField{Field: f, re: re})
	}
	return c, nil
}

// Matches reports whether a message is one this rule is for: from the rule's
// sender, and carrying the subject/body markers the rule insists on.
func (c *Compiled) Matches(fromEmail, fromDomain, subject, text string) bool {
	r := c.Rule
	fromEmail = strings.ToLower(strings.TrimSpace(fromEmail))
	if fromDomain == "" {
		if at := strings.LastIndex(fromEmail, "@"); at != -1 {
			fromDomain = fromEmail[at+1:]
		}
	}
	fromDomain = strings.ToLower(strings.TrimSpace(fromDomain))

	switch {
	case r.SenderEmail != "":
		if fromEmail != strings.ToLower(strings.TrimSpace(r.SenderEmail)) {
			return false
		}
	case r.SenderDomain != "":
		d := strings.ToLower(strings.TrimSpace(r.SenderDomain))
		if fromDomain != d && !strings.HasSuffix(fromDomain, "."+d) {
			return false
		}
	default:
		return false
	}
	if r.SubjectContains != "" && !strings.Contains(strings.ToLower(subject), strings.ToLower(r.SubjectContains)) {
		return false
	}
	if r.BodyContains != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(r.BodyContains)) {
		return false
	}
	return true
}

// Extraction is what a rule read out of one message, by field name. Values
// are the raw matched text; the typed accessors parse them.
type Extraction map[string]string

func (e Extraction) Amount(name string) float64 { return emailparse.ParseAmount(e[name]) }
func (e Extraction) Number(name string) float64 { return emailparse.ParseAmount(e[name]) }
func (e Extraction) Text(name string) string    { return strings.TrimSpace(e[name]) }

// Date returns the field as YYYY-MM-DD, or "" when it is absent or unreadable.
func (e Extraction) Date(name string) string {
	if v := e[name]; v != "" {
		return emailparse.ParseAlertDate(v)
	}
	return ""
}

// Apply extracts the rule's values from a message's text (SampleText output).
// It fails, naming the value, when something the rule's kind cannot do
// without is missing — the caller then treats the message as not matched by
// this rule rather than writing a half-read record.
func (c *Compiled) Apply(text string) (Extraction, error) {
	ext := Extraction{}
	for _, f := range c.fields {
		if v, ok := f.extract(text); ok {
			ext[f.Name] = v
		}
	}
	if err := checkRequired(c.Rule, ext); err != nil {
		return nil, err
	}
	return ext, nil
}

func (f *compiledField) extract(text string) (string, bool) {
	matches := f.re.FindAllStringSubmatchIndex(text, -1)
	idx := 0
	if f.Occurrence > 0 {
		idx = f.Occurrence - 1
	}
	if idx >= len(matches) {
		return "", false
	}
	m := matches[idx]
	if m[2] < 0 {
		return "", false
	}
	v := strings.TrimSpace(text[m[2]:m[3]])
	return v, v != ""
}

func checkRequired(r *Rule, ext Extraction) error {
	bound := r.AccountID != 0
	switch r.Kind {
	case KindTransaction:
		if ext.Amount("amount") <= 0 {
			return fmt.Errorf("could not read the amount")
		}
		if !bound && ext["account_last4"] == "" {
			return fmt.Errorf("could not read the account's last digits")
		}
	case KindBill:
		if !bound && ext["card_last4"] == "" {
			return fmt.Errorf("could not read the card's last digits")
		}
		if ext["total_due"] == "" && ext["minimum_due"] == "" && ext["due_date"] == "" {
			return fmt.Errorf("could not read a total due, a minimum due or a due date")
		}
	case KindBalance:
		if ext["balance"] == "" {
			return fmt.Errorf("could not read the balance")
		}
		if !bound && ext["account_last4"] == "" {
			return fmt.Errorf("could not read the account's last digits")
		}
	case KindTrade:
		if ext["symbol"] == "" && ext["identifier"] == "" {
			return fmt.Errorf("could not read the instrument")
		}
		if ext.Amount("amount") <= 0 && !(ext.Number("units") > 0 && ext.Number("price") > 0) {
			return fmt.Errorf("could not read the order amount, or its units and price")
		}
	}
	return nil
}
