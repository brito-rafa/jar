// Package catalog extracts explicitly filename-derived source metadata.
package catalog

import (
	"fmt"
	"golang.org/x/text/unicode/norm"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Origin struct{ Label, Path, Title, Kind, Date, Precision, RawCode, SearchKey string }

var conferenceCode = regexp.MustCompile(`^(\d{2})(\d{2})(\d{2})([A-Za-z]{2})(?:$|[-_ ])`)
var yearOnlyCode = regexp.MustCompile(`^(\d{2})00[-_]\d+[A-Za-z]{2}$`)
var fullDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})[-_]?([0-9]{2})[-_]?([0-9]{2})(?:[^0-9]|$)`)

func Normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Legacy six-digit conference codes follow YYMMDD with a 1900s century.
// This is a filename convention, not a verified event/publication date.
func Parse(label string) Origin {
	p := label
	if i := strings.LastIndex(p, " | "); i >= 0 {
		p = p[:i]
	}
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	stem := strings.TrimSuffix(base, path.Ext(base))
	o := Origin{Label: label, Path: p, Title: strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(stem)), " "), Kind: "document"}
	if strings.EqualFold(path.Ext(base), ".pdf") {
		o.Kind = "book/document"
	}
	if m := conferenceCode.FindStringSubmatch(stem); m != nil {
		o.Kind = "conference"
		o.RawCode = m[0]
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		o.Date, o.Precision = filenameDate(1900+year, month, day)
	} else if m := yearOnlyCode.FindStringSubmatch(stem); m != nil {
		year, _ := strconv.Atoi(m[1])
		o.Kind = "conference"
		o.RawCode = stem
		o.Date, o.Precision = filenameDate(1900+year, 0, 0)
	} else if m := fullDate.FindStringSubmatch(stem); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		o.RawCode = m[0]
		o.Date, o.Precision = filenameDate(year, month, day)
	}
	o.SearchKey = Normalize(o.Path + " " + o.Title)
	return o
}
func filenameDate(year, month, day int) (string, string) {
	if month == 0 && day == 0 {
		return fmt.Sprintf("%04d", year), "year"
	}
	if month < 1 || month > 12 {
		return "", ""
	}
	if day == 0 {
		return fmt.Sprintf("%04d-%02d", year, month), "month"
	}
	date := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", ""
	}
	return date, "day"
}
func (o Origin) Description() string {
	desc := fmt.Sprintf("%s [filename title: %s; type: %s", o.Label, o.Title, o.Kind)
	if o.Date != "" {
		desc += fmt.Sprintf("; filename date: %s (%s precision, inferred)", o.Date, o.Precision)
	}
	return desc + "]"
}

// ValidateDate accepts explicit ISO year/month/day precision, never guessed dates.
func ValidateDate(value string) error {
	format := ""
	switch len(value) {
	case 4:
		format = "2006"
	case 7:
		format = "2006-01"
	case 10:
		format = "2006-01-02"
	default:
		return fmt.Errorf("use YYYY, YYYY-MM, or YYYY-MM-DD")
	}
	if _, err := time.Parse(format, value); err != nil {
		return fmt.Errorf("invalid date: use YYYY, YYYY-MM, or YYYY-MM-DD")
	}
	return nil
}
