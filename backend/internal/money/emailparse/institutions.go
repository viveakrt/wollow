package emailparse

import "strings"

// AccountKind is what sort of thing an institution's alert is about. It maps
// straight onto finance_accounts.account_type, and getting it right is what
// keeps a savings-account alert from creating a credit card — which is what
// happened when every unmatched alert was assumed to be a card.
type AccountKind string

const (
	KindBank       AccountKind = "bank"
	KindCreditCard AccountKind = "credit_card"
	KindWallet     AccountKind = "wallet"
	KindInvestment AccountKind = "investment"
	KindLoan       AccountKind = "loan"
)

// Institution is one sender the Money product knows how to attribute mail to.
//
// Issuer is the short code the per-issuer parsers and the bills table key on
// and must stay stable; Name is what the UI shows. DefaultKind is only a
// starting guess — a sender that handles both a bank account and a card (most
// Indian banks) has its kind refined per message by KindForAlert.
type Institution struct {
	Issuer      string
	Name        string
	Domains     []string
	DefaultKind AccountKind
}

// institutions is the registry of senders whose mail is finance mail.
//
// This list is the *candidate selector*, not a parser whitelist: a sender here
// gets its bodies pulled and run through the parsers, and anything the parsers
// can't read still lands as 'unrecognized' rather than vanishing. Adding a
// domain therefore costs nothing but a little bandwidth, which is why it is
// deliberately broad.
var institutions = []Institution{
	// ---- Banks (each also issues cards; KindForAlert decides per message) ----
	{"HDFC", "HDFC Bank", []string{"hdfcbank.bank.in", "hdfcbank.com", "hdfcbank.net"}, KindBank},
	{"ICICI", "ICICI Bank", []string{"icici.bank.in", "icicibank.com", "icicibank.net"}, KindBank},
	{"Axis", "Axis Bank", []string{"axis.bank.in", "axisbank.com", "axisbank.co.in"}, KindBank},
	{"SBI", "State Bank of India", []string{"sbi.co.in", "alerts.sbi.co.in", "onlinesbi.com", "sbicard.com"}, KindBank},
	{"Kotak", "Kotak Mahindra Bank", []string{"kotak.com", "kotakbank.com", "kotak.bank.in"}, KindBank},
	{"IDFC", "IDFC FIRST Bank", []string{"idfcfirstbank.com", "idfcbank.com"}, KindBank},
	{"IndusInd", "IndusInd Bank", []string{"indusind.com"}, KindBank},
	{"Yes", "Yes Bank", []string{"yesbank.in"}, KindBank},
	{"PNB", "Punjab National Bank", []string{"pnb.co.in"}, KindBank},
	{"BoB", "Bank of Baroda", []string{"bankofbaroda.com", "bankofbaroda.co.in"}, KindBank},
	{"Canara", "Canara Bank", []string{"canarabank.com"}, KindBank},
	{"Union", "Union Bank of India", []string{"unionbankofindia.com"}, KindBank},
	{"Federal", "Federal Bank", []string{"federalbank.co.in"}, KindBank},
	{"RBL", "RBL Bank", []string{"rblbank.com"}, KindBank},
	{"AU", "AU Small Finance Bank", []string{"aubank.in"}, KindBank},
	{"Bandhan", "Bandhan Bank", []string{"bandhanbank.com"}, KindBank},
	{"DBS", "DBS Bank India", []string{"dbs.com"}, KindBank},

	// ---- Card issuers ----
	{"BOBCARD", "BOBCARD", []string{"bobcard.co.in", "bobfinancial.com"}, KindCreditCard},
	{"AmEx", "American Express", []string{"americanexpress.com", "aexp.com"}, KindCreditCard},
	{"HSBC", "HSBC India", []string{"hsbc.co.in"}, KindCreditCard},
	{"OneCard", "OneCard", []string{"getonecard.app"}, KindCreditCard},
	{"Slice", "Slice", []string{"sliceit.com"}, KindCreditCard},

	// ---- Wallets / prepaid / UPI apps ----
	{"AmazonPay", "Amazon Pay", []string{"amazonpay.in", "amazon.in"}, KindWallet},
	{"Paytm", "Paytm", []string{"paytm.com", "paytmbank.com", "paytm.in"}, KindWallet},
	{"PhonePe", "PhonePe", []string{"phonepe.com"}, KindWallet},
	{"GooglePay", "Google Pay", []string{"google.com"}, KindWallet},
	{"Mobikwik", "MobiKwik", []string{"mobikwik.com"}, KindWallet},
	{"Freecharge", "Freecharge", []string{"freecharge.com"}, KindWallet},
	{"Jupiter", "Jupiter", []string{"jupiter.money"}, KindWallet},
	{"Fi", "Fi Money", []string{"fi.money"}, KindWallet},

	// ---- Investments ----
	{"Zerodha", "Zerodha", []string{"zerodha.com", "zerodha.net"}, KindInvestment},
	{"Groww", "Groww", []string{"groww.in"}, KindInvestment},
	{"Upstox", "Upstox", []string{"upstox.com"}, KindInvestment},
	{"AngelOne", "Angel One", []string{"angelone.in", "angelbroking.com"}, KindInvestment},
	{"Kuvera", "Kuvera", []string{"kuvera.in"}, KindInvestment},
	{"CAMS", "CAMS", []string{"camsonline.com"}, KindInvestment},
	{"KFintech", "KFintech", []string{"kfintech.com"}, KindInvestment},
	{"NSDL", "NSDL", []string{"nsdl.co.in", "nsdl.com"}, KindInvestment},
	{"CDSL", "CDSL", []string{"cdslindia.com", "cdslindia.co.in"}, KindInvestment},
	{"MFCentral", "MF Central", []string{"mfcentral.com"}, KindInvestment},
	{"INDmoney", "INDmoney", []string{"indmoney.com", "indwealth.in"}, KindInvestment},
	{"PaytmMoney", "Paytm Money", []string{"paytmmoney.com"}, KindInvestment},
	{"EPFO", "EPFO", []string{"epfindia.gov.in", "epfoindia.gov.in"}, KindInvestment},
	{"NPS", "NPS / NSDL CRA", []string{"cra-nsdl.com", "npscra.nsdl.co.in"}, KindInvestment},

	// ---- Lenders ----
	{"BajajFinserv", "Bajaj Finserv", []string{"bajajfinserv.in", "bajajfinservmarkets.in"}, KindLoan},
	{"HDB", "HDB Financial Services", []string{"hdbfs.com"}, KindLoan},
	{"HomeCredit", "Home Credit", []string{"homecredit.co.in"}, KindLoan},
}

// byDomain indexes the registry for lookup, built once at init because
// IssuerForSender runs per message during ingest.
var byDomain = func() map[string]*Institution {
	index := make(map[string]*Institution)
	for i := range institutions {
		for _, domain := range institutions[i].Domains {
			index[strings.ToLower(domain)] = &institutions[i]
		}
	}
	return index
}()

// InstitutionForSender resolves a sender address (or bare domain) to the
// institution that owns it, matching subdomains too — banks send from
// alerts.hdfcbank.com as readily as hdfcbank.com. Returns nil for anything
// unrecognized.
func InstitutionForSender(fromAddress string) *Institution {
	domain := strings.ToLower(strings.TrimSpace(fromAddress))
	if at := strings.LastIndex(domain, "@"); at != -1 {
		domain = domain[at+1:]
	}
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return nil
	}

	// Walk from the full host up through its parents, so a match on
	// "hdfcbank.com" is found for "emailstatements.cards.hdfcbank.com" while
	// an unrelated "notbank.com" never is.
	for {
		if inst, ok := byDomain[domain]; ok {
			return inst
		}
		dot := strings.Index(domain, ".")
		if dot == -1 {
			return nil
		}
		domain = domain[dot+1:]
	}
}

// KnownInstitutions returns the registry for the UI to offer when someone adds
// an account by hand.
//
// It exists because the issuer code is load-bearing: alerts attach to an
// account by matching finance_accounts.bank against it, so an account typed in
// as "HDFC Bank Ltd" when the parsers say "HDFC" would never receive its own
// mail. Offering the list removes the guess.
func KnownInstitutions() []Institution {
	out := make([]Institution, len(institutions))
	copy(out, institutions)
	return out
}

// IssuerForSender maps a known bank/card sender domain to its short issuer
// code, or "" if the sender isn't recognized.
func IssuerForSender(fromAddress string) string {
	if inst := InstitutionForSender(fromAddress); inst != nil {
		return inst.Issuer
	}
	return ""
}

// DisplayNameForIssuer returns the human-readable institution name for a short
// issuer code, falling back to the code itself for issuers that only ever came
// from a statement file.
func DisplayNameForIssuer(issuer string) string {
	for i := range institutions {
		if institutions[i].Issuer == issuer {
			return institutions[i].Name
		}
	}
	return issuer
}

// cardWords and loanWords are the vocabulary that tells which product a
// message from a multi-product sender is actually about.
var (
	cardWords = []string{
		"credit card", "creditcard", "card no", "card ending", "card number",
		"statement for your", "available limit", "credit limit", "e-statement",
		"total amount due", "minimum amount due", "reward point",
	}
	loanWords = []string{"emi", "loan account", "loan a/c", "installment due", "instalment due"}
)

// KindForAlert refines an institution's default kind using the message itself.
//
// Most Indian banks send savings-account alerts, card alerts and loan reminders
// from one address, so the sender alone can't decide: "INR 1018 spent on credit
// card no. XX5792" from a bank is a card, and a balance alert from a card
// issuer is still a card. Only the bank default is refined upward — a wallet or
// broker saying "card" doesn't make it one.
func KindForAlert(inst *Institution, subject, body string) AccountKind {
	if inst == nil {
		return KindBank
	}
	if inst.DefaultKind != KindBank {
		return inst.DefaultKind
	}

	haystack := strings.ToLower(subject + "\n" + truncateForScan(body))
	for _, word := range loanWords {
		if strings.Contains(haystack, word) {
			return KindLoan
		}
	}
	for _, word := range cardWords {
		if strings.Contains(haystack, word) {
			return KindCreditCard
		}
	}
	return KindBank
}

// truncateForScan caps how much body text the keyword scans read. Alert mail
// puts its substance in the first screenful; the rest is boilerplate, footers
// and marketing that only produce false positives ("apply for a credit card
// today").
func truncateForScan(body string) string {
	const scanLimit = 1500
	if len(body) <= scanLimit {
		return body
	}
	return body[:scanLimit]
}

// AllowedSenderDomains is the set of sender domains known to carry finance
// mail. Money ingest uses it to pick candidates out of the shared message
// index; it is not a fetch filter, so a domain missing here only means the AI
// classifier has to be the one to flag the message.
var AllowedSenderDomains = func() []string {
	out := make([]string, 0, len(byDomain))
	for domain := range byDomain {
		out = append(out, domain)
	}
	return out
}()
