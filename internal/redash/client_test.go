package redash

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRequests(t *testing.T) {
	var calls []string
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordAPICall(t, w, r, &calls, &bodies)
	}))
	defer server.Close()

	client := New(server.URL+"/", "secret")
	ctx := context.Background()

	list, err := client.ListDataSources(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "analytics" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	got, err := client.GetDataSource(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Options["passwd"] != SecretPlaceholder || got.Groups[2] != false || got.Groups[3] != true {
		t.Fatalf("get = %+v", got)
	}
	port, ok := got.Options["port"].(float64)
	if !ok || port != 3306 {
		t.Fatalf("port = %#v", got.Options["port"])
	}

	created, err := client.CreateDataSource(ctx, "analytics", "mysql", map[string]any{"passwd": "pw"})
	if err != nil || created.ID != 5 || created.Groups[1] != false {
		t.Fatalf("create = %+v, %v", created, err)
	}

	updated, err := client.UpdateDataSource(ctx, 1, "analytics", "mysql", map[string]any{"ssl_mode": "required"})
	if err != nil || updated.ID != 1 {
		t.Fatalf("update = %+v, %v", updated, err)
	}

	testResult, err := client.TestDataSource(ctx, 1)
	if err != nil || testResult.OK || !strings.Contains(testResult.Message, "1045") {
		t.Fatalf("test = %+v, %v", testResult, err)
	}

	groups, err := client.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Type != "builtin" {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	group, err := client.CreateGroup(ctx, "writers")
	if err != nil || group.ID != 9 {
		t.Fatalf("create group = %+v, %v", group, err)
	}
	if err := client.AddDataSourceToGroup(ctx, 9, 5); err != nil {
		t.Fatal(err)
	}
	if err := client.SetDataSourcePermission(ctx, 9, 5, true); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveDataSourceFromGroup(ctx, 9, 5); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /api/data_sources",
		"GET /api/data_sources/1",
		"POST /api/data_sources",
		"POST /api/data_sources/1",
		"POST /api/data_sources/1/test",
		"GET /api/groups",
		"POST /api/groups",
		"POST /api/groups/9/data_sources",
		"POST /api/groups/9/data_sources/5",
		"DELETE /api/groups/9/data_sources/5",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls =\n%s", strings.Join(calls, "\n"))
	}
	sent := strings.Join(bodies, "\n")
	for _, fragment := range []string{
		`"passwd":"pw"`,
		`"type":"mysql"`,
		`"ssl_mode":"required"`,
		`"name":"writers"`,
		`"data_source_id":5`,
		`"view_only":true`,
	} {
		if !strings.Contains(sent, fragment) {
			t.Errorf("request bodies missing %s:\n%s", fragment, sent)
		}
	}
}

func recordAPICall(t *testing.T, w http.ResponseWriter, r *http.Request, calls, bodies *[]string) {
	t.Helper()
	if r.Header.Get("Authorization") != "Key secret" {
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
	key := r.Method + " " + r.URL.Path
	*calls = append(*calls, key)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	*bodies = append(*bodies, string(raw))

	payload, ok := apiPayloads[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, _ = w.Write([]byte(payload))
}

var apiPayloads = map[string]string{
	"GET /api/data_sources":               `[{"id":1,"name":"analytics","type":"mysql"}]`,
	"GET /api/data_sources/1":             `{"id":1,"name":"analytics","type":"mysql","options":{"host":"db","port":3306,"passwd":"--------","use_ssl":true,"ssl_mode":"required"},"groups":{"2":false,"3":true}}`,
	"POST /api/data_sources":              `{"id":5,"name":"analytics","type":"mysql","options":{"passwd":"--------"},"groups":{"1":false}}`,
	"POST /api/data_sources/1":            `{"id":1,"name":"analytics","type":"mysql","options":{"passwd":"--------"},"groups":{"2":false}}`,
	"POST /api/data_sources/1/test":       `{"ok":false,"message":"(1045, \"Access denied for user 'u'@'h' (using password: YES)\")"}`,
	"GET /api/groups":                     `[{"id":2,"name":"default","type":"builtin","permissions":["view_query"]}]`,
	"POST /api/groups":                    `{"id":9,"name":"writers","type":"regular"}`,
	"POST /api/groups/9/data_sources":     `{"id":5,"view_only":false}`,
	"POST /api/groups/9/data_sources/5":   `{"id":5,"view_only":true}`,
	"DELETE /api/groups/9/data_sources/5": "",
}

func TestClientHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Data source with the name analytics already exists."}`))
	}))
	defer server.Close()

	_, err := New(server.URL, "secret").CreateDataSource(context.Background(), "analytics", "mysql", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("err = %v", err)
	}
}
