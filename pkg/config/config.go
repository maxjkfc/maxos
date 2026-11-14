// Package config reads the process configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is the process configuration. The MAXOS_* variables it is read from
// are listed in .env.example.
type Config struct {
	DatabaseURL      string
	HTTPAddr         string
	AccessTeamDomain string
	AccessAUD        string
}

// Load reads the environment. Access settings are required only when
// requireAccess is true (the `serve` command). All missing required variables
// are reported together.
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
	return c, nil
}
