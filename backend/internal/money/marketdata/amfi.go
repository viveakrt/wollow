package marketdata

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NAV is one mutual fund scheme's latest net asset value.
type NAV struct {
	SchemeCode string
	Name       string
	Value      float64
	Date       string // YYYY-MM-DD
}

// AMFI reads the Association of Mutual Funds in India's daily NAV file, the official source for every Indian scheme.
type AMFI struct {
	HTTP *http.Client
	URL  string
}

func NewAMFI() *AMFI {
	return &AMFI{
		HTTP: &http.Client{Timeout: 60 * time.Second},
		URL:  "https://www.amfiindia.com/spages/NAVAll.txt",
	}
}

var isinRe = regexp.MustCompile(`^IN[A-Z0-9]{10}$`)

// NAVs downloads the day's file, indexed by ISIN.
func (a *AMFI) NAVs(ctx context.Context) (map[string]NAV, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AMFI returned HTTP %d", resp.StatusCode)
	}
	return ParseNAVAll(io.LimitReader(resp.Body, 32<<20))
}

// ParseNAVAll reads "Scheme Code;ISIN Growth;ISIN Reinvestment;Scheme Name;[Plan;Option;]NAV;Date" lines;
// NAV and date are read from the end so both the old and current column layouts parse.
func ParseNAVAll(r io.Reader) (map[string]NAV, error) {
	out := map[string]NAV{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), ";")
		if len(cols) < 6 {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(cols[len(cols)-2]), 64)
		if err != nil || value <= 0 {
			continue
		}
		date, err := time.Parse("02-Jan-2006", strings.TrimSpace(cols[len(cols)-1]))
		if err != nil {
			continue
		}
		nav := NAV{
			SchemeCode: strings.TrimSpace(cols[0]),
			Name:       strings.TrimSpace(cols[3]),
			Value:      value,
			Date:       date.Format("2006-01-02"),
		}
		for _, isin := range cols[1:3] {
			if isin = strings.TrimSpace(isin); isinRe.MatchString(isin) {
				out[isin] = nav
			}
		}
	}
	return out, sc.Err()
}
