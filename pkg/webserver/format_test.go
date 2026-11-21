package webserver_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/pkg/webserver"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestFormatAmount(t *testing.T) {
	tests := []struct {
		amount   string
		decimals int
		want     string
	}{
		{"0", 2, "0.00"},
		{"0", 0, "0"},
		{"5", 2, "5.00"},
		{"123", 2, "123.00"},
		{"1234", 2, "1,234.00"},
		{"1234.5", 2, "1,234.50"},
		{"123456.789", 2, "123,456.79"},
		{"1234567", 0, "1,234,567"},
		{"999999.995", 2, "1,000,000.00"},
		{"12345678901234567890.12", 2, "12,345,678,901,234,567,890.12"},
		{"0.00000001", 8, "0.00000001"},
		{"1234.56789012", 8, "1,234.56789012"},
		{"100", 3, "100.000"},
		{"-1", 2, "\u22121.00"},
		{"-1234.5", 2, "\u22121,234.50"},
		{"-1000000", 0, "\u22121,000,000"},
		// What rounds to nothing is nothing: no minus sign in front of a zero.
		{"-0.001", 2, "0.00"},
		{"0.004", 2, "0.00"},
		// A count of places that cannot be one is none.
		{"12.5", -1, "13"},
	}
	for _, tt := range tests {
		t.Run(tt.amount+"/"+tt.want, func(t *testing.T) {
			if got := webserver.FormatAmount(dec(tt.amount), tt.decimals); got != tt.want {
				t.Errorf("FormatAmount(%s, %d) = %q, want %q", tt.amount, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestFormatSigned(t *testing.T) {
	tests := []struct {
		amount   string
		decimals int
		want     string
	}{
		{"120", 2, "+120.00"},
		{"-120", 2, "\u2212120.00"},
		{"0", 2, "0.00"},
		{"0.004", 2, "0.00"},
		{"1234567.1", 1, "+1,234,567.1"},
	}
	for _, tt := range tests {
		if got := webserver.FormatSigned(dec(tt.amount), tt.decimals); got != tt.want {
			t.Errorf("FormatSigned(%s, %d) = %q, want %q", tt.amount, tt.decimals, got, tt.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		part, whole, want string
	}{
		{"1", "4", "25.0%"},
		{"1", "3", "33.3%"},
		{"2", "3", "66.7%"},
		{"5", "5", "100.0%"},
		{"0", "5", "0.0%"},
		{"7", "0", "—"},
		{"12345", "1000000", "1.2%"},
	}
	for _, tt := range tests {
		if got := webserver.FormatPercent(dec(tt.part), dec(tt.whole)); got != tt.want {
			t.Errorf("FormatPercent(%s, %s) = %q, want %q", tt.part, tt.whole, got, tt.want)
		}
	}
}

func TestFormatDates(t *testing.T) {
	// A date is kept at midnight UTC; the day is the day it is, in whatever zone the
	// user lives in.
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if got := webserver.FormatDate(day); got != "2026-11-14" {
		t.Errorf("FormatDate = %q", got)
	}
	if got := webserver.FormatMonth(day); got != "2026-11" {
		t.Errorf("FormatMonth = %q", got)
	}

	// A moment is read by the clock of the user: 23:30 UTC on the 14th is the 15th in
	// Taipei.
	taipei := time.FixedZone("UTC+8", 8*3600)
	moment := time.Date(2026, 11, 14, 23, 30, 0, 0, time.UTC)
	if got := webserver.FormatMoment(moment, taipei); got != "2026-11-15 07:30" {
		t.Errorf("FormatMoment = %q, want the clock of Taipei", got)
	}
	if got := webserver.FormatMoment(moment, time.UTC); got != "2026-11-14 23:30" {
		t.Errorf("FormatMoment in UTC = %q", got)
	}
}
