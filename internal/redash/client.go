// Package redash is an HTTP client for the Redash admin API used to manage data sources and groups.
package redash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SecretPlaceholder is the value Redash returns instead of a data source password.
// Sending it back on update keeps the stored password.
const SecretPlaceholder = "--------"

const httpTimeout = 2 * time.Minute

// DataSource is a Redash data source. Options and Groups are populated by get, create, and update.
type DataSource struct {
	ID      int
	Name    string
	Type    string
	Options map[string]any
	Groups  map[int]bool
}

// Group is a Redash group. Type is "builtin" or "regular".
type Group struct {
	ID   int
	Name string
	Type string
}

// TestResult is the response of the data source connection test.
type TestResult struct {
	OK      bool
	Message string
}

// Client calls the Redash API with an admin user API key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a client for baseURL. The key is sent as "Authorization: Key <apiKey>".
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: httpTimeout},
	}
}

type dataSourceDTO struct {
	ID      int             `json:"id"`
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Options map[string]any  `json:"options"`
	Groups  map[string]bool `json:"groups"`
}

type dataSourceBody struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Options map[string]any `json:"options"`
}

type groupDTO struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type testDTO struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ListDataSources returns data sources visible to the API key. The list payload has no options.
func (c *Client) ListDataSources(ctx context.Context) ([]DataSource, error) {
	var payload []dataSourceDTO
	if err := c.do(ctx, http.MethodGet, "/api/data_sources", nil, &payload); err != nil {
		return nil, err
	}
	out := make([]DataSource, 0, len(payload))
	for _, item := range payload {
		ds, err := item.dataSource()
		if err != nil {
			return nil, err
		}
		out = append(out, ds)
	}
	return out, nil
}

// GetDataSource returns one data source, including masked options and group access.
func (c *Client) GetDataSource(ctx context.Context, id int) (DataSource, error) {
	var payload dataSourceDTO
	if err := c.do(ctx, http.MethodGet, "/api/data_sources/"+strconv.Itoa(id), nil, &payload); err != nil {
		return DataSource{}, err
	}
	return payload.dataSource()
}

// CreateDataSource creates a data source. Redash also links it to the builtin default group.
func (c *Client) CreateDataSource(ctx context.Context, name, dsType string, options map[string]any) (DataSource, error) {
	var payload dataSourceDTO
	body := dataSourceBody{Name: name, Type: dsType, Options: options}
	if err := c.do(ctx, http.MethodPost, "/api/data_sources", body, &payload); err != nil {
		return DataSource{}, err
	}
	return payload.dataSource()
}

// UpdateDataSource replaces data source options. A password of SecretPlaceholder keeps the stored password.
func (c *Client) UpdateDataSource(ctx context.Context, id int, name, dsType string, options map[string]any) (DataSource, error) {
	var payload dataSourceDTO
	body := dataSourceBody{Name: name, Type: dsType, Options: options}
	if err := c.do(ctx, http.MethodPost, "/api/data_sources/"+strconv.Itoa(id), body, &payload); err != nil {
		return DataSource{}, err
	}
	return payload.dataSource()
}

// TestDataSource checks the saved connection. Redash waits for the test job before responding.
func (c *Client) TestDataSource(ctx context.Context, id int) (TestResult, error) {
	var payload testDTO
	if err := c.do(ctx, http.MethodPost, "/api/data_sources/"+strconv.Itoa(id)+"/test", map[string]any{}, &payload); err != nil {
		return TestResult{}, err
	}
	return TestResult(payload), nil
}

// ListGroups returns every group in the organization.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var payload []groupDTO
	if err := c.do(ctx, http.MethodGet, "/api/groups", nil, &payload); err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(payload))
	for _, item := range payload {
		out = append(out, Group(item))
	}
	return out, nil
}

// CreateGroup creates a regular group. Redash assigns its default permissions.
func (c *Client) CreateGroup(ctx context.Context, name string) (Group, error) {
	var payload groupDTO
	if err := c.do(ctx, http.MethodPost, "/api/groups", map[string]string{"name": name}, &payload); err != nil {
		return Group{}, err
	}
	return Group(payload), nil
}

// AddDataSourceToGroup links a data source to a group with full access.
func (c *Client) AddDataSourceToGroup(ctx context.Context, groupID, dataSourceID int) error {
	path := "/api/groups/" + strconv.Itoa(groupID) + "/data_sources"
	return c.do(ctx, http.MethodPost, path, map[string]int{"data_source_id": dataSourceID}, nil)
}

// SetDataSourcePermission sets view_only for an existing group link.
func (c *Client) SetDataSourcePermission(ctx context.Context, groupID, dataSourceID int, viewOnly bool) error {
	path := "/api/groups/" + strconv.Itoa(groupID) + "/data_sources/" + strconv.Itoa(dataSourceID)
	return c.do(ctx, http.MethodPost, path, map[string]bool{"view_only": viewOnly}, nil)
}

// RemoveDataSourceFromGroup unlinks a data source from a group.
func (c *Client) RemoveDataSourceFromGroup(ctx context.Context, groupID, dataSourceID int) error {
	path := "/api/groups/" + strconv.Itoa(groupID) + "/data_sources/" + strconv.Itoa(dataSourceID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (d dataSourceDTO) dataSource() (DataSource, error) {
	groups := make(map[int]bool, len(d.Groups))
	for rawID, viewOnly := range d.Groups {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			return DataSource{}, fmt.Errorf("data source %d: group id %q", d.ID, rawID)
		}
		groups[id] = viewOnly
	}
	return DataSource{
		ID:      d.ID,
		Name:    d.Name,
		Type:    d.Type,
		Options: d.Options,
		Groups:  groups,
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Key "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(data))
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decode: %w", method, path, err)
	}
	return nil
}
