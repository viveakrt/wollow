package mail

import "testing"

func TestParseUnsubscribeHeaders(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantNil bool
		http    string
		mailto  string
		oneClk  bool
	}{
		{
			name:   "http and mailto with one-click",
			raw:    "Subject: hi\r\nList-Unsubscribe: <https://example.com/unsub?id=1>, <mailto:unsub@example.com>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n",
			http:   "https://example.com/unsub?id=1",
			mailto: "mailto:unsub@example.com",
			oneClk: true,
		},
		{
			name:   "mailto only",
			raw:    "Subject: hi\r\nList-Unsubscribe: <mailto:unsub@example.com>\r\n",
			mailto: "mailto:unsub@example.com",
		},
		{
			name:    "no header at all",
			raw:     "Subject: hi\r\nFrom: a@b.com\r\n",
			wantNil: true,
		},
		{
			name:    "header present but no usable uri",
			raw:     "Subject: hi\r\nList-Unsubscribe: not-a-uri\r\n",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseUnsubscribeHeaders([]byte(tt.raw))
			if tt.wantNil {
				if info != nil {
					t.Fatalf("got %+v, want nil", info)
				}
				return
			}
			if info == nil {
				t.Fatal("got nil, want a result")
			}
			if info.HTTPURL != tt.http {
				t.Errorf("HTTPURL = %q, want %q", info.HTTPURL, tt.http)
			}
			if info.Mailto != tt.mailto {
				t.Errorf("Mailto = %q, want %q", info.Mailto, tt.mailto)
			}
			if info.OneClick != tt.oneClk {
				t.Errorf("OneClick = %v, want %v", info.OneClick, tt.oneClk)
			}
		})
	}
}
