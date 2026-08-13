package emailparser

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// AllowedSenderDomains is the sender allowlist used to decide which inbox
// emails are worth scanning at all — anything not from one of these domains
// is inbox noise and is never fetched. Editable/extendable as more
// banks/card issuers are supported.
var AllowedSenderDomains = []string{
	HDFCSenderDomain,
	AxisSenderDomain,
	"icici.bank.in",
	"bobcard.co.in",
}

// FetchedMessage is one email pulled from IMAP, still raw (undecoded).
type FetchedMessage struct {
	UID uint32
	Raw []byte
}

// FetchNewFromAllowlist connects to the given IMAP server, logs in, and
// returns every message in INBOX from an allowlisted sender with UID
// greater than sinceUID (0 fetches all matching mail — first sync). The
// connection is closed before returning.
func FetchNewFromAllowlist(host string, port int, email, appPassword string, sinceUID uint32) ([]FetchedMessage, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer client.Close()

	if err := client.Login(email, appPassword).Wait(); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select INBOX: %w", err)
	}

	var all []FetchedMessage
	for _, domain := range AllowedSenderDomains {
		criteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "From", Value: domain},
			},
		}
		if sinceUID > 0 {
			criteria.UID = []imap.UIDSet{{imap.UIDRange{Start: imap.UID(sinceUID + 1), Stop: 0}}}
		}

		searchData, err := client.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("search %s: %w", domain, err)
		}
		uids := searchData.AllUIDs()
		if len(uids) == 0 {
			continue
		}

		uidSet := imap.UIDSetNum(uids...)
		fetchOptions := &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{}},
		}
		messages, err := client.Fetch(uidSet, fetchOptions).Collect()
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", domain, err)
		}
		for _, msg := range messages {
			raw := msg.FindBodySection(&imap.FetchItemBodySection{})
			if raw == nil {
				continue
			}
			all = append(all, FetchedMessage{UID: uint32(msg.UID), Raw: raw})
		}
	}

	if err := client.Logout().Wait(); err != nil {
		return nil, fmt.Errorf("logout: %w", err)
	}

	return all, nil
}
