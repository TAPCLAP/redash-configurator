package sync

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"redash-configurator/internal/config"
	"redash-configurator/internal/redash"
)

func TestClassifyTest(t *testing.T) {
	cases := []struct {
		name   string
		result redash.TestResult
		want   string
	}{
		{name: "success", result: redash.TestResult{OK: true, Message: "success"}, want: testOK},
		{
			name:   "mysql access denied",
			result: redash.TestResult{OK: false, Message: `(1045, "Access denied for user 'auth_service_eu_redas'@'10.0.0.1' (using password: YES)")`},
			want:   testAccessDenied,
		},
		{name: "access denied without code", result: redash.TestResult{OK: false, Message: "ACCESS DENIED for user"}, want: testAccessDenied},
		{name: "cannot connect", result: redash.TestResult{OK: false, Message: `(2003, "Can't connect to MySQL server on 'db' (111)")`}, want: testOther},
		{name: "unknown database", result: redash.TestResult{OK: false, Message: `(1049, "Unknown database 'analytics'")`}, want: testOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTest(tc.result); got != tc.want {
				t.Fatalf("classify = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDriftedFields(t *testing.T) {
	src := sampleSource()
	current := redash.DataSource{
		Type: mysqlType,
		Options: map[string]any{
			"host":     "db.internal",
			"port":     float64(3306),
			"user":     "reader",
			"passwd":   redash.SecretPlaceholder,
			"db":       "analytics",
			"use_ssl":  true,
			"ssl_mode": sslModeRequired,
			"charset":  "utf8mb4",
		},
	}
	if fields := driftedFields(current, src); len(fields) != 0 {
		t.Fatalf("unexpected drift: %v", fields)
	}

	current.Options["host"] = "old"
	current.Options["ssl_mode"] = "preferred"
	delete(current.Options, "use_ssl")
	if fields := driftedFields(current, src); !slices.Equal(fields, []string{"host", "use_ssl", "ssl_mode"}) {
		t.Fatalf("fields = %v", fields)
	}

	withCert := sampleSource()
	withCert.SSLCACert = "/etc/ssl/ca.pem"
	current.Options["host"] = "db.internal"
	current.Options["use_ssl"] = true
	current.Options["ssl_mode"] = sslModeRequired
	if fields := driftedFields(current, withCert); !slices.Equal(fields, []string{"ssl_cacert"}) {
		t.Fatalf("cacert fields = %v", fields)
	}
}

func TestApplyOptions(t *testing.T) {
	src := sampleSource()
	src.SSLCACert = ""
	base := map[string]any{
		"charset":    "utf8mb4",
		"ssl_cacert": "/old/ca.pem",
		"passwd":     redash.SecretPlaceholder,
	}

	updated := applyOptions(base, src, true)
	if updated["passwd"] != "p@ss" || updated["host"] != "db.internal" || updated["port"] != 3306 || updated["db"] != "analytics" {
		t.Fatalf("updated = %#v", updated)
	}
	if updated["use_ssl"] != true || updated["ssl_mode"] != sslModeRequired || updated["charset"] != "utf8mb4" {
		t.Fatalf("ssl/charset = %#v", updated)
	}
	if _, ok := updated["ssl_cacert"]; ok {
		t.Fatal("empty CA certificate should be omitted")
	}
	if base["passwd"] != redash.SecretPlaceholder {
		t.Fatal("applyOptions mutated the current options")
	}

	kept := applyOptions(base, src, false)
	if kept["passwd"] != redash.SecretPlaceholder {
		t.Fatalf("passwd = %#v", kept["passwd"])
	}
}

func TestPlanGroupActions(t *testing.T) {
	byName := map[string]redash.Group{
		"default": {ID: 1, Name: "default", Type: "builtin"},
		"writers": {ID: 2, Name: "writers", Type: "regular"},
		"readers": {ID: 3, Name: "readers", Type: "regular"},
		"legacy":  {ID: 4, Name: "legacy", Type: "regular"},
	}
	byID := map[int]redash.Group{}
	for _, group := range byName {
		byID[group.ID] = group
	}

	actions, err := planGroupActions(
		map[int]bool{1: false, 2: true, 4: false, 9: false},
		byID,
		byName,
		[]string{"writers"},
		[]string{"readers"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []groupAction{
		{GroupID: 2, Name: "writers", Op: opSet, ViewOnly: false},
		{GroupID: 3, Name: "readers", Op: opAdd, ViewOnly: true},
		{GroupID: 3, Name: "readers", Op: opSet, ViewOnly: true},
		{GroupID: 4, Name: "legacy", Op: opRemove},
	}
	if !slices.Equal(actions, want) {
		t.Fatalf("actions = %+v", actions)
	}

	unchanged, err := planGroupActions(map[int]bool{1: false, 3: true}, byID, byName, nil, []string{"readers"})
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged) != 0 {
		t.Fatalf("builtin and matching read group should stay, got %+v", unchanged)
	}
}

func TestRunCreatesSourceAndGroups(t *testing.T) {
	api := &fakeAPI{
		groups: []redash.Group{{ID: 1, Name: "default", Type: "builtin"}},
		tests:  []redash.TestResult{{OK: true, Message: "success"}},
	}
	src := sampleSource()
	src.WriteGroups = []string{"writers"}
	src.ReadGroups = []string{"readers"}

	if err := New(api, []config.Source{src}, nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.created) != 1 {
		t.Fatalf("created = %d", len(api.created))
	}
	options := api.created[0].options
	if options["passwd"] != src.Password || options["use_ssl"] != true || options["ssl_mode"] != sslModeRequired {
		t.Fatalf("options = %#v", options)
	}
	if _, ok := options["ssl_cacert"]; ok {
		t.Fatal("ssl_cacert should be omitted")
	}
	if slices.Compare(api.createdGroups, []string{"readers", "writers"}) != 0 {
		t.Fatalf("groups = %#v", api.createdGroups)
	}
	if len(api.tests) != 0 || len(api.testIDs) != 1 {
		t.Fatalf("tests left = %d, calls = %v", len(api.tests), api.testIDs)
	}
	if len(api.added) != 2 || len(api.removed) != 0 {
		t.Fatalf("added = %+v removed = %+v", api.added, api.removed)
	}
	if len(api.perms) != 1 || !api.perms[0].viewOnly || api.perms[0].groupID != groupID(api, "readers") {
		t.Fatalf("perms = %+v", api.perms)
	}
}

func TestRunUpdatesChangedOptions(t *testing.T) {
	src := sampleSource()
	api := &fakeAPI{
		groups: []redash.Group{{ID: 1, Name: "default", Type: "builtin"}},
		sources: map[int]redash.DataSource{
			10: {
				ID:   10,
				Name: src.Name,
				Type: mysqlType,
				Options: map[string]any{
					"host":     "old.internal",
					"port":     float64(3306),
					"user":     src.User,
					"passwd":   redash.SecretPlaceholder,
					"db":       src.DBName,
					"use_ssl":  true,
					"ssl_mode": sslModeRequired,
					"charset":  "utf8mb4",
				},
				Groups: map[int]bool{1: false},
			},
		},
		tests: []redash.TestResult{{OK: true, Message: "success"}},
	}
	if err := New(api, []config.Source{src}, nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.updated) != 1 || len(api.created) != 0 || len(api.testIDs) != 1 {
		t.Fatalf("updated = %+v tests = %v", api.updated, api.testIDs)
	}
	options := api.updated[0].options
	if options["host"] != src.Host || options["passwd"] != src.Password || options["charset"] != "utf8mb4" {
		t.Fatalf("options = %#v", options)
	}
}

func TestRunPasswordCheck(t *testing.T) {
	src := sampleSource()
	matching := func() redash.DataSource {
		return redash.DataSource{
			ID:   10,
			Name: src.Name,
			Type: mysqlType,
			Options: map[string]any{
				"host":     src.Host,
				"port":     float64(src.Port),
				"user":     src.User,
				"passwd":   redash.SecretPlaceholder,
				"db":       src.DBName,
				"use_ssl":  true,
				"ssl_mode": sslModeRequired,
			},
			Groups: map[int]bool{1: false, 4: false},
		}
	}

	t.Run("valid password", func(t *testing.T) {
		api := &fakeAPI{
			groups:  []redash.Group{{ID: 1, Name: "default", Type: "builtin"}},
			sources: map[int]redash.DataSource{10: matching()},
			tests:   []redash.TestResult{{OK: true, Message: "success"}},
		}
		if err := New(api, []config.Source{src}, nil).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(api.updated) != 0 || len(api.testIDs) != 1 {
			t.Fatalf("updated = %d tests = %v", len(api.updated), api.testIDs)
		}
	})

	t.Run("access denied updates password", func(t *testing.T) {
		api := &fakeAPI{
			groups:  []redash.Group{{ID: 1, Name: "default", Type: "builtin"}},
			sources: map[int]redash.DataSource{10: matching()},
			tests: []redash.TestResult{
				{OK: false, Message: `(1045, "Access denied for user 'reader'@'10.0.0.8' (using password: YES)")`},
				{OK: true, Message: "success"},
			},
		}
		if err := New(api, []config.Source{src}, nil).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(api.updated) != 1 || api.updated[0].options["passwd"] != src.Password || len(api.testIDs) != 2 {
			t.Fatalf("updated = %#v tests = %v", api.updated, api.testIDs)
		}
	})

	t.Run("other test error keeps password", func(t *testing.T) {
		api := &fakeAPI{
			groups: []redash.Group{
				{ID: 1, Name: "default", Type: "builtin"},
				{ID: 4, Name: "legacy", Type: "regular"},
			},
			sources: map[int]redash.DataSource{10: matching()},
			tests:   []redash.TestResult{{OK: false, Message: `(2003, "Can't connect to MySQL server")`}},
		}
		err := New(api, []config.Source{src}, nil).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "Can't connect") {
			t.Fatalf("err = %v", err)
		}
		if len(api.updated) != 0 {
			t.Fatalf("password was updated: %#v", api.updated)
		}
		if len(api.removed) != 1 || api.removed[0].groupID != 4 {
			t.Fatalf("removed = %+v", api.removed)
		}
	})
}

func TestRunContinuesAfterSourceError(t *testing.T) {
	first := sampleSource()
	second := sampleSource()
	second.EnvID = "OTHER"
	second.Name = "Game"
	second.DBName = "game"

	api := &fakeAPI{
		groups: []redash.Group{{ID: 1, Name: "default", Type: "builtin"}},
		sources: map[int]redash.DataSource{
			10: storedSource(first, 10),
			11: storedSource(second, 11),
		},
		tests: []redash.TestResult{
			{OK: true, Message: "success"},
			{OK: false, Message: "unknown database"},
		},
	}
	err := New(api, []config.Source{first, second}, nil).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Game") || strings.Contains(err.Error(), "Analytics") {
		t.Fatalf("err = %v", err)
	}
	if len(api.testIDs) != 2 || len(api.updated) != 0 {
		t.Fatalf("tests = %v updated = %d", api.testIDs, len(api.updated))
	}
}

func sampleSource() config.Source {
	return config.Source{
		EnvID:    "BLABLA",
		Name:     "Analytics",
		Host:     "db.internal",
		Port:     3306,
		User:     "reader",
		Password: "p@ss",
		DBName:   "analytics",
		DBType:   mysqlType,
	}
}

func storedSource(src config.Source, id int) redash.DataSource {
	return redash.DataSource{
		ID:   id,
		Name: src.Name,
		Type: mysqlType,
		Options: map[string]any{
			"host":     src.Host,
			"port":     float64(src.Port),
			"user":     src.User,
			"passwd":   redash.SecretPlaceholder,
			"db":       src.DBName,
			"use_ssl":  true,
			"ssl_mode": sslModeRequired,
		},
		Groups: map[int]bool{1: false},
	}
}

func groupID(api *fakeAPI, name string) int {
	for _, group := range api.groups {
		if group.Name == name {
			return group.ID
		}
	}
	return 0
}

type linkCall struct {
	groupID int
	dsID    int
}

type permCall struct {
	groupID  int
	dsID     int
	viewOnly bool
}

type optionCall struct {
	id      int
	name    string
	options map[string]any
}

type fakeAPI struct {
	groups  []redash.Group
	sources map[int]redash.DataSource
	nextDS  int
	nextGrp int

	tests   []redash.TestResult
	testIDs []int

	created       []optionCall
	updated       []optionCall
	added         []linkCall
	perms         []permCall
	removed       []linkCall
	createdGroups []string
}

func (f *fakeAPI) ListDataSources(context.Context) ([]redash.DataSource, error) {
	out := make([]redash.DataSource, 0, len(f.sources))
	for _, ds := range f.sources {
		out = append(out, redash.DataSource{ID: ds.ID, Name: ds.Name, Type: ds.Type})
	}
	return out, nil
}

func (f *fakeAPI) GetDataSource(_ context.Context, id int) (redash.DataSource, error) {
	ds, ok := f.sources[id]
	if !ok {
		return redash.DataSource{}, fmt.Errorf("missing data source %d", id)
	}
	return cloneSource(ds), nil
}

func (f *fakeAPI) CreateDataSource(_ context.Context, name, dsType string, options map[string]any) (redash.DataSource, error) {
	if f.sources == nil {
		f.sources = map[int]redash.DataSource{}
	}
	f.nextDS++
	id := 100 + f.nextDS
	groups := map[int]bool{}
	for _, group := range f.groups {
		if group.Type == "builtin" && group.Name == "default" {
			groups[group.ID] = false
		}
	}
	ds := redash.DataSource{ID: id, Name: name, Type: dsType, Options: cloneOptions(options), Groups: groups}
	f.sources[id] = ds
	f.created = append(f.created, optionCall{id: id, name: name, options: cloneOptions(options)})
	return cloneSource(ds), nil
}

func (f *fakeAPI) UpdateDataSource(_ context.Context, id int, name, dsType string, options map[string]any) (redash.DataSource, error) {
	ds, ok := f.sources[id]
	if !ok {
		return redash.DataSource{}, fmt.Errorf("missing data source %d", id)
	}
	ds.Name = name
	ds.Type = dsType
	ds.Options = cloneOptions(options)
	f.sources[id] = ds
	f.updated = append(f.updated, optionCall{id: id, name: name, options: cloneOptions(options)})
	return cloneSource(ds), nil
}

func (f *fakeAPI) TestDataSource(_ context.Context, id int) (redash.TestResult, error) {
	f.testIDs = append(f.testIDs, id)
	if len(f.tests) == 0 {
		return redash.TestResult{}, errors.New("unexpected connection test")
	}
	result := f.tests[0]
	f.tests = f.tests[1:]
	return result, nil
}

func (f *fakeAPI) ListGroups(context.Context) ([]redash.Group, error) {
	return append([]redash.Group(nil), f.groups...), nil
}

func (f *fakeAPI) CreateGroup(_ context.Context, name string) (redash.Group, error) {
	f.nextGrp++
	group := redash.Group{ID: 10 + f.nextGrp, Name: name, Type: "regular"}
	f.groups = append(f.groups, group)
	f.createdGroups = append(f.createdGroups, name)
	return group, nil
}

func (f *fakeAPI) AddDataSourceToGroup(_ context.Context, groupID, dataSourceID int) error {
	f.added = append(f.added, linkCall{groupID: groupID, dsID: dataSourceID})
	return nil
}

func (f *fakeAPI) SetDataSourcePermission(_ context.Context, groupID, dataSourceID int, viewOnly bool) error {
	f.perms = append(f.perms, permCall{groupID: groupID, dsID: dataSourceID, viewOnly: viewOnly})
	return nil
}

func (f *fakeAPI) RemoveDataSourceFromGroup(_ context.Context, groupID, dataSourceID int) error {
	f.removed = append(f.removed, linkCall{groupID: groupID, dsID: dataSourceID})
	return nil
}

func cloneSource(ds redash.DataSource) redash.DataSource {
	groups := make(map[int]bool, len(ds.Groups))
	maps.Copy(groups, ds.Groups)
	ds.Options = cloneOptions(ds.Options)
	ds.Groups = groups
	return ds
}

func cloneOptions(options map[string]any) map[string]any {
	out := make(map[string]any, len(options))
	maps.Copy(out, options)
	return out
}
