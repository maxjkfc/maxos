// Package config reads the process configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// The timezone database is embedded in the binary, so that the zone the
	// application is configured with loads on a machine that has none, such as the
	// runtime image.
	_ "time/tzdata"
)

// DefaultTimezone is the timezone of the application when MAXOS_TIMEZONE is not
// set.
const DefaultTimezone = "Asia/Taipei"

// Config is the process configuration. The MAXOS_* variables it is read from
// are listed in .env.example.
type Config struct {
	DatabaseURL      string
	HTTPAddr         string
	AccessTeamDomain string
	AccessAUD        string
	// Timezone is where the user lives: it decides what day it is, so which
	// transactions have already happened and which are still to come.
	Timezone *time.Location
}

// Load reads the environment. Access settings are required only when
// requireAccess is true (the `serve` command). All missing required variables
// are reported together. MAXOS_TIMEZONE, when set, must name a timezone of the
// IANA database.
func Load(requireAccess bool) (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("MAXOS_DATABASE_URL"),
		HTTPAddr:         os.Getenv("MAXOS_HTTP_ADDR"),
		AccessTeamDomain: strings.TrimRight(os.Getenv("MAXOS_ACCESS_TEAM_DOMAIN"), "/"),
		AccessAUD:        os.Getenv("MAXOS_ACCESS_AUD"),
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}

	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "MAXOS_DATABASE_URL")
	}
	if requireAccess {
		if c.AccessTeamDomain == "" {
			missing = append(missing, "MAXOS_ACCESS_TEAM_DOMAIN")
		}
		if c.AccessAUD == "" {
			missing = append(missing, "MAXOS_ACCESS_AUD")
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	zone := strings.TrimSpace(os.Getenv("MAXOS_TIMEZONE"))
	if zone == "" {
		zone = DefaultTimezone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Config{}, fmt.Errorf("MAXOS_TIMEZONE %q: %w", zone, err)
	}
	c.Timezone = loc
	return c, nil
}

// MozeDirVariable names the environment variable that says where the files of a MOZE
// import are.
const MozeDirVariable = "MAXOS_MOZE_DIR"

// MozeDir is the directory that holds the files of a MOZE import: the export
// (MOZE_*.csv, the newest of them), the mapping (mapping.yaml) and the rates
// (rates.csv). MAXOS_MOZE_DIR, when set, names it; otherwise it is
// ~/.config/maxos/moze. It is read apart from Load, which needs the database: the
// plan of an import is made with none.
func MozeDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(MozeDirVariable)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%s is not set, and the home directory is not known: %w", MozeDirVariable, err)
	}
	return filepath.Join(home, ".config", "maxos", "moze"), nil
}
