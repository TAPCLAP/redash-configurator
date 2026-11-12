package config

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func TestParseSourcesFromNameKeys(t *testing.T) {
	cfg, err := Parse(map[string]string{
		"REDASH_URL":                 "https://redash.example.com/",
		"REDASH_API_KEY":             "secret",
		"SOURCE_BLABLA_NAME":         "Analytics EU",
		"SOURCE_BLABLA_HOST":         "db.internal",
		"SOURCE_BLABLA_PORT":         "3306",
		"SOURCE_BLABLA_USER":         "reader",
		"SOURCE_BLABLA_PASSWORD":     "p@ss",
		"SOURCE_BLABLA_DBNAME":       "analytics",
		"SOURCE_BLABLA_DBTYPE":       "MySQL",
		"SOURCE_BLABLA_SSL_CACERT":   "/etc/ssl/ca.pem",
		"SOURCE_BLABLA_WRITE_GROUPS": " writers, writers, editors ",
		"SOURCE_BLABLA_READ_GROUPS":  "viewers,,",
		"SOURCE_OTHER_NAME":          "Game",
		"SOURCE_OTHER_HOST":          "game.internal",
		"SOURCE_OTHER_PORT":          "3307",
		"SOURCE_OTHER_USER":          "game",
		"SOURCE_OTHER_PASSWORD":      "secret",
		"SOURCE_OTHER_DBNAME":        "game",
		"SOURCE_OTHER_DBTYPE":        "mysql",
		// Must not be treated as a source name key.
		"SOURCE_BLABLA_DBNAME_EXTRA": "ignored",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.RedashURL != "https://redash.example.com" {
		t.Fatalf("url = %q", cfg.RedashURL)
	}
	if cfg.SyncInterval != 5*time.Minute {
		t.Fatalf("interval = %s", cfg.SyncInterval)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("sources = %d", len(cfg.Sources))
	}

	blabla := cfg.Sources[0]
	if blabla.EnvID != "BLABLA" || blabla.Name != "Analytics EU" {
		t.Fatalf("first source = %+v", blabla)
	}
	if blabla.Host != "db.internal" || blabla.Port != 3306 || blabla.User != "reader" || blabla.Password != "p@ss" || blabla.DBName != "analytics" {
		t.Fatalf("connection = %+v", blabla)
	}
	if blabla.DBType != "mysql" || blabla.SSLCACert != "/etc/ssl/ca.pem" {
		t.Fatalf("ssl/type = %+v", blabla)
	}
	if strings.Join(blabla.WriteGroups, ",") != "writers,editors" {
		t.Fatalf("write groups = %#v", blabla.WriteGroups)
	}
	if strings.Join(blabla.ReadGroups, ",") != "viewers" {
		t.Fatalf("read groups = %#v", blabla.ReadGroups)
	}
	if cfg.Sources[1].EnvID != "OTHER" || cfg.Sources[1].Name != "Game" || cfg.Sources[1].SSLCACert != "" {
		t.Fatalf("second source = %+v", cfg.Sources[1])
	}
}

func TestParseRejectsInvalidConfig(t *testing.T) {
	base := map[string]string{
		"REDASH_URL":             "https://redash.example.com",
		"REDASH_API_KEY":         "secret",
		"SOURCE_BLABLA_NAME":     "Analytics",
		"SOURCE_BLABLA_HOST":     "db.internal",
		"SOURCE_BLABLA_PORT":     "3306",
		"SOURCE_BLABLA_USER":     "reader",
		"SOURCE_BLABLA_PASSWORD": "p@ss",
		"SOURCE_BLABLA_DBNAME":   "analytics",
		"SOURCE_BLABLA_DBTYPE":   "mysql",
	}

	t.Run("no sources", func(t *testing.T) {
		_, err := Parse(map[string]string{
			"REDASH_URL":           "https://redash.example.com",
			"REDASH_API_KEY":       "secret",
			"SOURCE_BLABLA_DBNAME": "analytics",
		})
		if err == nil || !strings.Contains(err.Error(), "no data sources") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("duplicate names", func(t *testing.T) {
		env := clone(base)
		env["SOURCE_OTHER_NAME"] = "Analytics"
		env["SOURCE_OTHER_HOST"] = "other"
		env["SOURCE_OTHER_PORT"] = "3306"
		env["SOURCE_OTHER_USER"] = "u"
		env["SOURCE_OTHER_PASSWORD"] = "p"
		env["SOURCE_OTHER_DBNAME"] = "db"
		env["SOURCE_OTHER_DBTYPE"] = "mysql"
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), "both SOURCE_BLABLA_NAME and SOURCE_OTHER_NAME") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("group overlap", func(t *testing.T) {
		env := clone(base)
		env["SOURCE_BLABLA_WRITE_GROUPS"] = "analysts"
		env["SOURCE_BLABLA_READ_GROUPS"] = "viewers, analysts"
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), `group "analysts" is in both`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unsupported dbtype", func(t *testing.T) {
		env := clone(base)
		env["SOURCE_BLABLA_DBTYPE"] = "postgres"
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), "only mysql is supported") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad port", func(t *testing.T) {
		env := clone(base)
		env["SOURCE_BLABLA_PORT"] = "0"
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), "SOURCE_BLABLA_PORT") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing url", func(t *testing.T) {
		env := clone(base)
		delete(env, "REDASH_URL")
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), "REDASH_URL") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad interval", func(t *testing.T) {
		env := clone(base)
		env["SYNC_INTERVAL"] = "nope"
		_, err := Parse(env)
		if err == nil || !strings.Contains(err.Error(), "SYNC_INTERVAL") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseSyncInterval(t *testing.T) {
	env := map[string]string{
		"REDASH_URL":        "http://localhost:5000",
		"REDASH_API_KEY":    "secret",
		"SYNC_INTERVAL":     "30s",
		"SOURCE_A_NAME":     "A",
		"SOURCE_A_HOST":     "h",
		"SOURCE_A_PORT":     "3306",
		"SOURCE_A_USER":     "u",
		"SOURCE_A_PASSWORD": "p",
		"SOURCE_A_DBNAME":   "d",
		"SOURCE_A_DBTYPE":   "mysql",
	}
	cfg, err := Parse(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SyncInterval != 30*time.Second {
		t.Fatalf("interval = %s", cfg.SyncInterval)
	}
}

func clone(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}
