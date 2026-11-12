// Package config loads the configurator settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSyncInterval = 5 * time.Minute
	mysqlType           = "mysql"
)

// sourceNameKey matches SOURCE_<ID>_NAME and not SOURCE_<ID>_DBNAME.
var sourceNameKey = regexp.MustCompile(`^SOURCE_([A-Z0-9_]+)_NAME$`)

// Config is the process configuration.
type Config struct {
	RedashURL    string
	APIKey       string
	SyncInterval time.Duration
	Sources      []Source
}

// Source is one MySQL data source. EnvID is the SOURCE_<ID> key and is not the Redash name.
type Source struct {
	EnvID       string
	Name        string
	Host        string
	Port        int
	User        string
	Password    string
	DBName      string
	DBType      string
	SSLCACert   string
	WriteGroups []string
	ReadGroups  []string
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return Parse(env)
}

// Parse builds a Config from an environment map.
func Parse(env map[string]string) (Config, error) {
	var errs []error

	rawURL := strings.TrimSpace(env["REDASH_URL"])
	redashURL, urlErr := parseRedashURL(rawURL)
	if urlErr != nil {
		errs = append(errs, urlErr)
	}

	apiKey := strings.TrimSpace(env["REDASH_API_KEY"])
	if apiKey == "" {
		errs = append(errs, errors.New("REDASH_API_KEY is required"))
	}

	interval := defaultSyncInterval
	if raw := strings.TrimSpace(env["SYNC_INTERVAL"]); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("SYNC_INTERVAL: %w", err))
		} else if parsed <= 0 {
			errs = append(errs, errors.New("SYNC_INTERVAL must be greater than zero"))
		} else {
			interval = parsed
		}
	}

	ids := sourceIDs(env)
	sources := make([]Source, 0, len(ids))
	for _, id := range ids {
		src, err := parseSource(env, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sources = append(sources, src)
	}
	if len(ids) == 0 {
		errs = append(errs, errors.New("no data sources found: set SOURCE_<ID>_NAME"))
	}
	errs = append(errs, duplicateNames(sources)...)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return Config{
		RedashURL:    redashURL,
		APIKey:       apiKey,
		SyncInterval: interval,
		Sources:      sources,
	}, nil
}

func parseRedashURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("REDASH_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("REDASH_URL must be an absolute http or https URL")
	}
	return strings.TrimRight(raw, "/"), nil
}

func sourceIDs(env map[string]string) []string {
	ids := make([]string, 0)
	for key := range env {
		match := sourceNameKey.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		ids = append(ids, match[1])
	}
	sort.Strings(ids)
	return ids
}

func parseSource(env map[string]string, id string) (Source, error) {
	var errs []error
	require := func(field string) string {
		value := env[envKey(id, field)]
		if field != "PASSWORD" {
			value = strings.TrimSpace(value)
		}
		if value == "" {
			errs = append(errs, fmt.Errorf("%s is required", envKey(id, field)))
		}
		return value
	}

	name := require("NAME")
	host := require("HOST")
	user := require("USER")
	password := require("PASSWORD")
	dbName := require("DBNAME")
	dbType := require("DBTYPE")
	portRaw := require("PORT")

	port := 0
	if portRaw != "" {
		parsed, err := strconv.Atoi(portRaw)
		if err != nil || parsed < 1 || parsed > 65535 {
			errs = append(errs, fmt.Errorf("%s must be a TCP port", envKey(id, "PORT")))
		} else {
			port = parsed
		}
	}

	if dbType != "" && !strings.EqualFold(dbType, mysqlType) {
		errs = append(errs, fmt.Errorf("%s: only mysql is supported", envKey(id, "DBTYPE")))
	} else if dbType != "" {
		dbType = mysqlType
	}

	writeGroups := splitGroups(env[envKey(id, "WRITE_GROUPS")])
	readGroups := splitGroups(env[envKey(id, "READ_GROUPS")])
	if overlap := groupOverlap(writeGroups, readGroups); overlap != "" {
		errs = append(errs, fmt.Errorf("%s: group %q is in both write and read groups", envKey(id, "NAME"), overlap))
	}

	if len(errs) > 0 {
		return Source{}, errors.Join(errs...)
	}
	return Source{
		EnvID:       id,
		Name:        name,
		Host:        host,
		Port:        port,
		User:        user,
		Password:    password,
		DBName:      dbName,
		DBType:      dbType,
		SSLCACert:   strings.TrimSpace(env[envKey(id, "SSL_CACERT")]),
		WriteGroups: writeGroups,
		ReadGroups:  readGroups,
	}, nil
}

func envKey(id, field string) string {
	return "SOURCE_" + id + "_" + field
}

func splitGroups(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func groupOverlap(write, read []string) string {
	seen := make(map[string]struct{}, len(write))
	for _, name := range write {
		seen[name] = struct{}{}
	}
	for _, name := range read {
		if _, ok := seen[name]; ok {
			return name
		}
	}
	return ""
}

func duplicateNames(sources []Source) []error {
	seen := make(map[string]string, len(sources))
	var errs []error
	for _, src := range sources {
		if prev, ok := seen[src.Name]; ok {
			errs = append(errs, fmt.Errorf("data source name %q is set by both SOURCE_%s_NAME and SOURCE_%s_NAME", src.Name, prev, src.EnvID))
			continue
		}
		seen[src.Name] = src.EnvID
	}
	return errs
}
