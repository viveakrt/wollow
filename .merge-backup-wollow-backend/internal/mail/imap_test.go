package mail

import "testing"

func TestMakeSnippet(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantNot []string
	}{
		{
			name: "plain text passes through",
			raw:  "Dear Customer, 098617 is SECRET OTP for transaction of INR 200.75.",
			want: "Dear Customer, 098617 is SECRET OTP for transaction of INR 200.75.",
		},
		{
			name: "multipart mime headers and entity chains are stripped",
			raw: "------=_Part_17237933_2113055943.1718860130635\r\n" +
				"Content-Type: text/html; charset=UTF-8\r\n" +
				"Content-Transfer-Encoding: quoted-printable\r\n" +
				"\r\n" +
				"<html><body><p>Your card was charged INR 200.</p></body></html>\r\n",
			want:    "Your card was charged INR 200.",
			wantNot: []string{"Content-Type", "Part_17237933", "<html>"},
		},
		{
			name: "css and style blocks are dropped",
			raw: "<style>@media screen and (max-width:699px){.lihide{display:none!important}}</style>" +
				"<div>Your Thursday morning trip with Uber</div>",
			want:    "Your Thursday morning trip with Uber",
			wantNot: []string{"@media", "display:none", "lihide"},
		},
		{
			name:    "quoted printable is decoded",
			raw:     "Total =3D INR 200=2E75 for your ride",
			want:    "Total = INR 200.75 for your ride",
			wantNot: []string{"=3D", "=2E"},
		},
		{
			name: "html entities are decoded",
			raw:  "<p>Tom &amp; Jerry&#39;s &quot;order&quot;&nbsp;shipped</p>",
			want: `Tom & Jerry's "order" shipped`,
		},
		{
			name: "mime preamble line is dropped",
			raw: "This is a multipart message in MIME format.\r\n" +
				"------=_Part_1\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"Dear Student, thank you for registering.",
			want:    "Dear Student, thank you for registering.",
			wantNot: []string{"multipart message"},
		},
		{
			name: "empty input yields empty snippet",
			raw:  "",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := makeSnippet([]byte(tc.raw))
			if got != tc.want {
				t.Errorf("makeSnippet()\n got: %q\nwant: %q", got, tc.want)
			}
			for _, bad := range tc.wantNot {
				if contains(got, bad) {
					t.Errorf("snippet should not contain %q, got %q", bad, got)
				}
			}
		})
	}
}

func TestMakeSnippetIsCapped(t *testing.T) {
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'a'
	}
	if got := makeSnippet(long); len(got) > maxSnippet {
		t.Errorf("snippet length = %d, want <= %d", len(got), maxSnippet)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
