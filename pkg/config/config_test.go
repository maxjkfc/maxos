package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/pkg/config"
)

func TestLoadReadsTheTimezoneFromTheEnvironment(t *testing.T) {
	tests := []struct {
		name string
		zone string
		want string
	}{
		{"a zone of the IANA database", "America/New_York", "America/New_York"},
		{"a zone with spaces around it", "  Europe/Paris ", "Europe/Paris"},
		{"UTC", "UTC", "UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MAXOS_DATABASE_URL", "postgres://example")
			t.Setenv("MAXOS_TIMEZONE", tt.zone)

			cfg, err := config.Load(false)

			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Timezone == nil || cfg.Timezone.String() != tt.want {
				t.Fatalf("Timezone = %v, want %s", cfg.Timezone, tt.want)
			}
		})
	}
}

func TestLoadTakesTheDefaultTimezoneWhenNoneIsSet(t *testing.T) {
	for _, tt := range []struct{ name, zone string }{{"empty", ""}, {"only spaces", "   "}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MAXOS_DATABASE_URL", "postgres://example")
			t.Setenv("MAXOS_TIMEZONE", tt.zone)

			cfg, err := config.Load(false)

			if err != nil {
				t.Fatalf("Load: %v, want the default zone to load: the variable is optional", err)
			}
			want, err := time.LoadLocation(config.DefaultTimezone)
			if err != nil {
				t.Fatalf("DefaultTimezone %q is not a zone of the IANA database: %v", config.DefaultTimezone, err)
			}
			if cfg.Timezone == nil || cfg.Timezone.String() != want.String() {
				t.Fatalf("Timezone = %v, want the default, %s", cfg.Timezone, want)
			}
			// The user lives in Taiwan, which is eight hours ahead of UTC all the year
			// round: the default is what puts the user's day on the calendar.
			if _, offset := time.Date(2026, time.July, 1, 0, 0, 0, 0, cfg.Timezone).Zone(); offset != 8*60*60 {
				t.Errorf("the default zone is %d seconds from UTC, want 8 hours", offset)
			}
		})
	}
}

func TestLoadRefusesATimezoneThatIsNotOne(t *testing.T) {
	for _, zone := range []string{"Mars/Olympus_Mons", "UTC+8", "not a zone"} {
		t.Run(zone, func(t *testing.T) {
			t.Setenv("MAXOS_DATABASE_URL", "postgres://example")
			t.Setenv("MAXOS_TIMEZONE", zone)

			_, err := config.Load(false)

			if err == nil {
				t.Fatalf("Load took %q for a timezone", zone)
			}
			if !strings.Contains(err.Error(), "MAXOS_TIMEZONE") || !strings.Contains(err.Error(), zone) {
				t.Errorf("err = %q, want it to name the variable and the value it refused", err)
			}
		})
	}
}
