// Package sync reconciles Redash MySQL data sources and group access with the desired configuration.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"sort"
	"strings"

	"redash-configurator/internal/config"
	"redash-configurator/internal/redash"
)

const (
	mysqlType       = "mysql"
	sslModeRequired = "required"
	groupBuiltin    = "builtin"

	testOK           = "ok"
	testAccessDenied = "access-denied"
	testOther        = "other"

	opAdd    = "add"
	opSet    = "set"
	opRemove = "remove"
)

// API is the subset of the Redash client the sync uses.
type API interface {
	ListDataSources(ctx context.Context) ([]redash.DataSource, error)
	GetDataSource(ctx context.Context, id int) (redash.DataSource, error)
	CreateDataSource(ctx context.Context, name, dsType string, options map[string]any) (redash.DataSource, error)
	UpdateDataSource(ctx context.Context, id int, name, dsType string, options map[string]any) (redash.DataSource, error)
	TestDataSource(ctx context.Context, id int) (redash.TestResult, error)
	ListGroups(ctx context.Context) ([]redash.Group, error)
	CreateGroup(ctx context.Context, name string) (redash.Group, error)
	AddDataSourceToGroup(ctx context.Context, groupID, dataSourceID int) error
	SetDataSourcePermission(ctx context.Context, groupID, dataSourceID int, viewOnly bool) error
	RemoveDataSourceFromGroup(ctx context.Context, groupID, dataSourceID int) error
}

// Syncer applies the desired sources on each pass.
type Syncer struct {
	api     API
	sources []config.Source
	log     *slog.Logger
}

// New returns a Syncer. A nil logger uses slog.Default.
func New(api API, sources []config.Source, log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	return &Syncer{api: api, sources: sources, log: log}
}

// Run syncs every configured source once. One source failing does not stop the others.
func (s *Syncer) Run(ctx context.Context) error {
	groupsByName, groupsByID, err := s.ensureGroups(ctx)
	if err != nil {
		return err
	}
	list, err := s.api.ListDataSources(ctx)
	if err != nil {
		return err
	}
	byName, problems := indexDataSources(list, managedNames(s.sources))

	var errs []error
	for _, src := range s.sources {
		if err := problems[src.Name]; err != nil {
			s.log.Error("source sync failed", "source", src.Name, "env_id", src.EnvID, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", src.Name, err))
			continue
		}
		if err := s.syncSource(ctx, src, byName, groupsByName, groupsByID); err != nil {
			s.log.Error("source sync failed", "source", src.Name, "env_id", src.EnvID, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", src.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Syncer) syncSource(ctx context.Context, src config.Source, byName map[string]redash.DataSource, groupsByName map[string]redash.Group, groupsByID map[int]redash.Group) error {
	var ds redash.DataSource
	var testErr error

	current, found := byName[src.Name]
	if !found {
		created, err := s.api.CreateDataSource(ctx, src.Name, mysqlType, applyOptions(nil, src, true))
		if err != nil {
			return err
		}
		s.log.Info("created data source", "source", src.Name, "data_source_id", created.ID)
		ds = created
		testErr = s.testConnection(ctx, src.Name, created.ID)
	} else {
		full, err := s.api.GetDataSource(ctx, current.ID)
		if err != nil {
			return err
		}
		fields := driftedFields(full, src)
		if len(fields) > 0 {
			updated, err := s.api.UpdateDataSource(ctx, full.ID, src.Name, mysqlType, applyOptions(full.Options, src, true))
			if err != nil {
				return err
			}
			s.log.Info("updated data source", "source", src.Name, "data_source_id", updated.ID, "fields", fields)
			ds = updated
			testErr = s.testConnection(ctx, src.Name, updated.ID)
		} else {
			ds, testErr = s.confirmPassword(ctx, src, full)
		}
	}

	groupErr := s.syncGroups(ctx, ds, src, groupsByName, groupsByID)
	return errors.Join(testErr, groupErr)
}

func (s *Syncer) confirmPassword(ctx context.Context, src config.Source, full redash.DataSource) (redash.DataSource, error) {
	result, err := s.api.TestDataSource(ctx, full.ID)
	if err != nil {
		return full, err
	}
	switch classifyTest(result) {
	case testOK:
		s.log.Info("data source up to date", "source", src.Name, "data_source_id", full.ID)
		return full, nil
	case testAccessDenied:
		s.log.Info("stored password rejected, updating", "source", src.Name, "data_source_id", full.ID)
		updated, err := s.api.UpdateDataSource(ctx, full.ID, src.Name, mysqlType, applyOptions(full.Options, src, true))
		if err != nil {
			return full, err
		}
		return updated, s.testConnection(ctx, src.Name, updated.ID)
	default:
		s.log.Error("connection test failed", "source", src.Name, "data_source_id", full.ID, "message", result.Message)
		return full, fmt.Errorf("connection test: %s", result.Message)
	}
}

func (s *Syncer) testConnection(ctx context.Context, name string, id int) error {
	result, err := s.api.TestDataSource(ctx, id)
	if err != nil {
		return err
	}
	if result.OK {
		s.log.Info("connection test succeeded", "source", name, "data_source_id", id)
		return nil
	}
	s.log.Error("connection test failed", "source", name, "data_source_id", id, "message", result.Message)
	return fmt.Errorf("connection test: %s", result.Message)
}

func (s *Syncer) ensureGroups(ctx context.Context) (map[string]redash.Group, map[int]redash.Group, error) {
	list, err := s.api.ListGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]redash.Group, len(list))
	byID := make(map[int]redash.Group, len(list))
	duplicates := make(map[string]struct{})
	for _, group := range list {
		byID[group.ID] = group
		if _, ok := byName[group.Name]; ok {
			duplicates[group.Name] = struct{}{}
			continue
		}
		byName[group.Name] = group
	}

	for _, name := range neededGroupNames(s.sources) {
		if _, dup := duplicates[name]; dup {
			return nil, nil, fmt.Errorf("redash has more than one group named %q", name)
		}
		if _, ok := byName[name]; ok {
			continue
		}
		created, err := s.api.CreateGroup(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		s.log.Info("created group", "group", created.Name, "group_id", created.ID)
		byName[created.Name] = created
		byID[created.ID] = created
	}
	return byName, byID, nil
}

func (s *Syncer) syncGroups(ctx context.Context, ds redash.DataSource, src config.Source, byName map[string]redash.Group, byID map[int]redash.Group) error {
	actions, err := planGroupActions(ds.Groups, byID, byName, src.WriteGroups, src.ReadGroups)
	if err != nil {
		return err
	}
	for _, action := range actions {
		switch action.Op {
		case opAdd:
			if err := s.api.AddDataSourceToGroup(ctx, action.GroupID, ds.ID); err != nil {
				return err
			}
			s.log.Info("added data source to group", "source", src.Name, "group", action.Name, "view_only", action.ViewOnly)
		case opSet:
			if err := s.api.SetDataSourcePermission(ctx, action.GroupID, ds.ID, action.ViewOnly); err != nil {
				return err
			}
			s.log.Info("updated group access", "source", src.Name, "group", action.Name, "view_only", action.ViewOnly)
		case opRemove:
			if err := s.api.RemoveDataSourceFromGroup(ctx, action.GroupID, ds.ID); err != nil {
				return err
			}
			s.log.Info("removed data source from group", "source", src.Name, "group", action.Name)
		}
	}
	return nil
}

type groupAction struct {
	GroupID  int
	Name     string
	Op       string
	ViewOnly bool
}

func planGroupActions(current map[int]bool, byID map[int]redash.Group, byName map[string]redash.Group, write, read []string) ([]groupAction, error) {
	desired := make(map[int]bool, len(write)+len(read))
	names := make(map[int]string, len(write)+len(read))
	for _, name := range write {
		group, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("group %q is missing", name)
		}
		desired[group.ID] = false
		names[group.ID] = group.Name
	}
	for _, name := range read {
		group, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("group %q is missing", name)
		}
		desired[group.ID] = true
		names[group.ID] = group.Name
	}

	var actions []groupAction
	for _, id := range sortedIDs(desired) {
		viewOnly := desired[id]
		currentView, linked := current[id]
		if !linked {
			actions = append(actions, groupAction{GroupID: id, Name: names[id], Op: opAdd, ViewOnly: viewOnly})
			if viewOnly {
				actions = append(actions, groupAction{GroupID: id, Name: names[id], Op: opSet, ViewOnly: true})
			}
			continue
		}
		if currentView != viewOnly {
			actions = append(actions, groupAction{GroupID: id, Name: names[id], Op: opSet, ViewOnly: viewOnly})
		}
	}

	for _, id := range sortedIDs(current) {
		if _, wanted := desired[id]; wanted {
			continue
		}
		group, known := byID[id]
		if !known || group.Type == groupBuiltin {
			continue
		}
		actions = append(actions, groupAction{GroupID: id, Name: group.Name, Op: opRemove})
	}
	return actions, nil
}

func classifyTest(result redash.TestResult) string {
	if result.OK {
		return testOK
	}
	message := strings.ToLower(result.Message)
	if strings.Contains(result.Message, "1045") || strings.Contains(message, "access denied") {
		return testAccessDenied
	}
	return testOther
}

func driftedFields(ds redash.DataSource, src config.Source) []string {
	var fields []string
	if ds.Type != mysqlType {
		fields = append(fields, "type")
	}
	if got, ok := optString(ds.Options, "host"); !ok || got != src.Host {
		fields = append(fields, "host")
	}
	if got, ok := optPort(ds.Options); !ok || got != src.Port {
		fields = append(fields, "port")
	}
	if got, ok := optString(ds.Options, "user"); !ok || got != src.User {
		fields = append(fields, "user")
	}
	if got, ok := optString(ds.Options, "db"); !ok || got != src.DBName {
		fields = append(fields, "db")
	}
	if got, ok := optBool(ds.Options, "use_ssl"); !ok || !got {
		fields = append(fields, "use_ssl")
	}
	if got, ok := optString(ds.Options, "ssl_mode"); !ok || got != sslModeRequired {
		fields = append(fields, "ssl_mode")
	}
	if got, ok := optString(ds.Options, "ssl_cacert"); !ok || got != src.SSLCACert {
		fields = append(fields, "ssl_cacert")
	}
	return fields
}

func applyOptions(base map[string]any, src config.Source, sendPassword bool) map[string]any {
	out := make(map[string]any, len(base)+8)
	maps.Copy(out, base)
	out["host"] = src.Host
	out["port"] = src.Port
	out["user"] = src.User
	out["db"] = src.DBName
	out["use_ssl"] = true
	out["ssl_mode"] = sslModeRequired
	if src.SSLCACert == "" {
		delete(out, "ssl_cacert")
	} else {
		out["ssl_cacert"] = src.SSLCACert
	}
	if sendPassword {
		out["passwd"] = src.Password
	} else {
		out["passwd"] = redash.SecretPlaceholder
	}
	return out
}

func optString(options map[string]any, key string) (string, bool) {
	value, ok := options[key]
	if !ok || value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func optBool(options map[string]any, key string) (bool, bool) {
	value, ok := options[key]
	if !ok || value == nil {
		return false, true
	}
	flag, ok := value.(bool)
	return flag, ok
}

func optPort(options map[string]any) (int, bool) {
	value, ok := options["port"]
	if !ok || value == nil {
		return 0, true
	}
	switch port := value.(type) {
	case int:
		return port, true
	case int64:
		return int(port), true
	case float64:
		if port != math.Trunc(port) {
			return 0, false
		}
		return int(port), true
	default:
		return 0, false
	}
}

func sortedIDs(values map[int]bool) []int {
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func neededGroupNames(sources []config.Source) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, src := range sources {
		for _, name := range append(append([]string{}, src.WriteGroups...), src.ReadGroups...) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func managedNames(sources []config.Source) map[string]struct{} {
	names := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		names[src.Name] = struct{}{}
	}
	return names
}

func indexDataSources(list []redash.DataSource, managed map[string]struct{}) (map[string]redash.DataSource, map[string]error) {
	byName := make(map[string]redash.DataSource, len(list))
	duplicates := map[string]struct{}{}
	for _, ds := range list {
		if _, ok := byName[ds.Name]; ok {
			duplicates[ds.Name] = struct{}{}
			continue
		}
		byName[ds.Name] = ds
	}
	problems := map[string]error{}
	for name := range duplicates {
		if _, ok := managed[name]; !ok {
			continue
		}
		problems[name] = fmt.Errorf("redash has more than one data source named %q", name)
		delete(byName, name)
	}
	return byName, problems
}
