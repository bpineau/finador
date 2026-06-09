package domain

import (
	"fmt"
	"time"
)

// Date is a civil day — no clock, no time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("date %q (attendu AAAA-MM-JJ): %w", s, err)
	}
	return DateOf(t), nil
}

func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

func Today() Date { return DateOf(time.Now()) }

func (d Date) String() string { return d.Time().Format("2006-01-02") }

// Time renders the date as midnight UTC, for arithmetic and ordering.
func (d Date) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) Before(o Date) bool { return d.Time().Before(o.Time()) }
func (d Date) IsZero() bool       { return d == Date{} }

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
