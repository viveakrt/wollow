package parsers

import "strings"

// keywordCategory is a best-effort default categorization applied to newly
// imported transactions, based on merchant/narration keywords. Users can
// always recategorize afterward; this just avoids everything landing in
// "Others".
var keywordCategory = []struct {
	keyword  string
	category string
}{
	{"ZOMATO", "Food & Dining"},
	{"SWIGGY", "Food & Dining"},
	{"BLINKIT", "Groceries"},
	{"ZEPTO", "Groceries"},
	{"BIGBASKET", "Groceries"},
	{"DMART", "Groceries"},
	{"UBER", "Transport"},
	{"OLA", "Transport"},
	{"RAPIDO", "Transport"},
	{"IRCTC", "Transport"},
	{"PETROL", "Transport"},
	{"FUEL", "Transport"},
	{"AMAZON", "Shopping"},
	{"FLIPKART", "Shopping"},
	{"MYNTRA", "Shopping"},
	{"AJIO", "Shopping"},
	{"ELECTRICITY", "Utilities"},
	{"BROADBAND", "Utilities"},
	{"RECHARGE", "Utilities"},
	{"JIO", "Utilities"},
	{"AIRTEL", "Utilities"},
	{"NETFLIX", "Entertainment"},
	{"PRIME VIDEO", "Entertainment"},
	{"HOTSTAR", "Entertainment"},
	{"SPOTIFY", "Entertainment"},
	{"BOOKMYSHOW", "Entertainment"},
	{"PHARMACY", "Health"},
	{"APOLLO", "Health"},
	{"HOSPITAL", "Health"},
	{"ZERODHA", "Investment"},
	{"GROWW", "Investment"},
	{"COIN ", "Investment"},
	{"MUTUAL FUND", "Investment"},
	{"INSURANCE", "Insurance"},
	{"LIC ", "Insurance"},
	{"SALARY", "Salary"},
	{"INTEREST PAID", "Interest"},
	{"REFUND", "Refund"},
}

// SuggestCategory returns a category name guess for a narration/merchant
// string, or "" if nothing matched (caller should default to "Others").
func SuggestCategory(narration string) string {
	upper := strings.ToUpper(narration)
	for _, kc := range keywordCategory {
		if strings.Contains(upper, kc.keyword) {
			return kc.category
		}
	}
	return ""
}
