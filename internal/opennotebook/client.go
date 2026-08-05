// Package opennotebook implements Midden's deliberately narrow P3 service
// integration.
//
// Open Notebook owns its own UI and data. Midden sends a prepared source only
// after an explicit operator action, then returns a deep link to that UI. It
// never mirrors notebooks or stores Open Notebook credentials in its index.
package opennotebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/plugins"
)

const (
	maxResponseBody = 2 << 20
	pollInterval    = 750 * time.Millisecond
)

// Client is built from one verified service manifest. It holds no credential:
// the optional password is resolved only immediately before a user-initiated
// request, never rendered in a plugin status response or persisted to disk.
type Client struct {
	base         *url.URL
	authHeader   string
	authScheme   string
	authEnv      string
	authOptional bool
	password     string
	link         string
	http         *http.Client
}

// Source is the normalized subset of Open Notebook's source response.
type Source struct {
	ID     string
	Status string
}

// Prepared is the action-ready subset of an Open Notebook manifest. It is
// built both by the UI status path and the export job so a green Send button
// cannot represent a contract the action will reject later.
type Prepared struct {
	Client *Client
	Push   plugins.Push
}

// Prepare validates the exact P3 action contract without sending anything.
func Prepare(m plugins.Manifest) (*Prepared, error) {
	client, err := New(m)
	if err != nil {
		return nil, err
	}
	var (
		push    plugins.Push
		hasPush bool
	)
	for _, candidate := range m.Push {
		if !hasPush || candidate.Name == "nuggets-as-source" {
			push = candidate
			hasPush = true
		}
	}
	if !hasPush {
		return nil, fmt.Errorf("Open Notebook manifest has no source push")
	}
	if push.Endpoint != "/sources" || push.Encoding != "multipart" {
		return nil, fmt.Errorf("Open Notebook source push must use multipart /sources")
	}
	if !strings.Contains(push.Fields["content"], "{{body}}") {
		return nil, fmt.Errorf("Open Notebook source push content must include {{body}}")
	}
	if !strings.Contains(push.Fields["notebooks"], "{{notebook_id}}") {
		return nil, fmt.Errorf("Open Notebook source push notebooks must include {{notebook_id}}")
	}
	if !strings.Contains(m.Link, "{{notebook_id|urlencode}}") {
		return nil, fmt.Errorf("Open Notebook UI link must include {{notebook_id|urlencode}}")
	}
	fields, err := plugins.RenderFields(push.Fields, map[string]string{
		"notebook_id": "notebook:validation",
		"title":       "validation",
		"body":        "validation",
	})
	if err != nil {
		return nil, err
	}
	if err := validateNotebookForm(fields, "notebook:validation"); err != nil {
		return nil, err
	}
	return &Prepared{Client: client, Push: push}, nil
}

// New constructs a client from a validated Open Notebook manifest.
func New(m plugins.Manifest) (*Client, error) {
	if m.Kind != "service" {
		return nil, fmt.Errorf("Open Notebook manifest must be a service")
	}
	if m.API.Base == "" || strings.Contains(m.API.Base, "${") {
		return nil, fmt.Errorf("Open Notebook API base must be a literal URL")
	}
	base, err := url.Parse(m.API.Base)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil ||
		(base.Scheme != "http" && base.Scheme != "https") || !isLoopbackURL(base) {
		return nil, fmt.Errorf("invalid Open Notebook API base")
	}
	hasHeader := strings.TrimSpace(m.API.Auth.Header) != ""
	hasEnv := strings.TrimSpace(m.API.Auth.Env) != ""
	if hasHeader != hasEnv {
		return nil, fmt.Errorf("Open Notebook auth header and env must be configured together")
	}
	if hasHeader {
		if !strings.EqualFold(m.API.Auth.Header, "Authorization") {
			return nil, fmt.Errorf("Open Notebook auth header must be Authorization")
		}
		if m.API.Auth.Env != "OPEN_NOTEBOOK_PASSWORD" {
			return nil, fmt.Errorf("Open Notebook auth env must be OPEN_NOTEBOOK_PASSWORD")
		}
		if m.API.Auth.Scheme != "" && !strings.EqualFold(m.API.Auth.Scheme, "Bearer") {
			return nil, fmt.Errorf("Open Notebook auth scheme must be Bearer")
		}
	} else if m.API.Auth.Scheme != "" {
		return nil, fmt.Errorf("Open Notebook auth scheme requires header and env")
	}
	if err := validateNotebookLink(m.Link); err != nil {
		return nil, err
	}
	client := &Client{
		base:         base,
		authHeader:   m.API.Auth.Header,
		authScheme:   m.API.Auth.Scheme,
		authEnv:      m.API.Auth.Env,
		authOptional: m.API.Auth.Optional,
		link:         m.Link,
		http:         exportHTTPClient(),
	}
	return client, nil
}

// exportHTTPClient deliberately ignores proxy environment variables. The
// payload can contain recovered evidence and local password auth, and all
// accepted Open Notebook targets are loopback-only.
func exportHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// WithPassword returns an action-scoped client. The password stays only in
// process memory for this request; it is never written to settings, the index,
// a job result, or a manifest.
func (c *Client) WithPassword(password string) *Client {
	copy := *c
	copy.password = password
	return &copy
}

// CreateTextSource sends a text source using the multipart contract Open
// Notebook requires even for plain text.
func (c *Client) CreateTextSource(ctx context.Context, push plugins.Push, notebookID, title, body string) (Source, error) {
	if push.Endpoint != "/sources" || push.Encoding != "multipart" {
		return Source{}, fmt.Errorf("Open Notebook source push must use multipart /sources")
	}
	if strings.TrimSpace(notebookID) == "" {
		return Source{}, fmt.Errorf("notebook id is required")
	}
	if strings.TrimSpace(body) == "" {
		return Source{}, fmt.Errorf("source body is empty")
	}

	fields, err := plugins.RenderFields(push.Fields, map[string]string{
		"notebook_id": notebookID,
		"title":       title,
		"body":        body,
	})
	if err != nil {
		return Source{}, err
	}
	if fields["type"] != "text" {
		return Source{}, fmt.Errorf("Open Notebook push type must be text")
	}
	if err := validateNotebookForm(fields, notebookID); err != nil {
		return Source{}, err
	}

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return Source{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return Source{}, err
	}

	endpoint, err := c.endpoint(push.Endpoint)
	if err != nil {
		return Source{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &payload)
	if err != nil {
		return Source{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if err := c.applyAuth(req); err != nil {
		return Source{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Source{}, fmt.Errorf("create source: %w", err)
	}
	defer resp.Body.Close()
	if !statusAllowed(resp.StatusCode, push.ExpectStatus) {
		if req.Header.Get(c.authHeader) != "" {
			return Source{}, authenticatedHTTPError(resp)
		}
		return Source{}, httpError(resp)
	}
	return decodeSource(resp.Body)
}

// WaitSource polls the source status endpoint after async ingestion. It stops
// at completed or failed, and context controls the outer job deadline.
func (c *Client) WaitSource(ctx context.Context, poll plugins.Poll, sourceID string, progress func(string)) (Source, error) {
	if poll.URL == "" {
		return Source{ID: sourceID, Status: "submitted"}, nil
	}
	path := strings.ReplaceAll(poll.URL, "{{source_id}}", url.PathEscape(sourceID))
	if strings.Contains(path, "{{") {
		return Source{}, fmt.Errorf("unresolved source poll placeholder")
	}
	endpoint, err := c.endpoint(path)
	if err != nil {
		return Source{}, err
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return Source{}, err
		}
		if err := c.applyAuth(req); err != nil {
			return Source{}, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return Source{}, fmt.Errorf("poll source: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			var err error
			if req.Header.Get(c.authHeader) != "" {
				err = authenticatedHTTPError(resp)
			} else {
				err = httpError(resp)
			}
			resp.Body.Close()
			return Source{}, err
		}
		source, err := decodeSourceStatus(resp.Body, sourceID)
		resp.Body.Close()
		if err != nil {
			return Source{}, err
		}
		if progress != nil {
			progress("Open Notebook source " + source.Status)
		}
		switch source.Status {
		case "completed":
			return source, nil
		case "failed":
			return source, fmt.Errorf("Open Notebook source processing failed")
		}
		select {
		case <-ctx.Done():
			return Source{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// decodeSourceStatus handles the lightweight /status response, which may
// contain only a status field. The caller already knows the source id from
// the create response, so requiring the endpoint to repeat it only forces a
// fallback to the much larger full source response.
func decodeSourceStatus(body io.Reader, sourceID string) (Source, error) {
	var raw struct {
		ID       string `json:"id"`
		SourceID string `json:"source_id"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&raw); err != nil {
		return Source{}, fmt.Errorf("decode Open Notebook source status: %w", err)
	}
	if raw.ID == "" {
		raw.ID = raw.SourceID
	}
	if raw.ID == "" {
		raw.ID = sourceID
	}
	if raw.Status == "" {
		return Source{}, fmt.Errorf("Open Notebook status response omitted status")
	}
	return Source{ID: raw.ID, Status: raw.Status}, nil
}

// NotebookLink returns the configured UI deep link. SurrealDB ids contain a
// colon, so the ID is path-escaped rather than concatenated raw.
func (c *Client) NotebookLink(notebookID string) (string, error) {
	if c.link == "" {
		return "", errors.New("Open Notebook manifest has no UI link template")
	}
	// PathEscape intentionally leaves ':' legal in a path segment, but Open
	// Notebook's Next.js route needs a SurrealDB record id as one encoded
	// segment (`notebook%3Aabc`), not a literal colon.
	escaped := strings.ReplaceAll(url.PathEscape(notebookID), ":", "%3A")
	link := strings.ReplaceAll(c.link, "{{notebook_id|urlencode}}", escaped)
	if err := validateNotebookLink(link); err != nil {
		return "", err
	}
	return link, nil
}

func (c *Client) endpoint(path string) (*url.URL, error) {
	u, err := url.Parse(path)
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return nil, fmt.Errorf("invalid Open Notebook endpoint %q", path)
	}
	out := *c.base
	out.Path = strings.TrimRight(c.base.Path, "/") + u.Path
	out.RawQuery = u.RawQuery
	return &out, nil
}

func (c *Client) applyAuth(req *http.Request) error {
	if c.authHeader == "" || c.authEnv == "" {
		return nil
	}
	if err := c.CredentialsReady(); err != nil {
		return err
	}
	value := c.password
	if value == "" {
		value, _ = os.LookupEnv(c.authEnv)
	}
	if value == "" {
		return nil
	}
	if c.authScheme != "" {
		value = c.authScheme + " " + value
	} else if strings.EqualFold(c.authHeader, "Authorization") {
		// Open Notebook's password middleware uses the standard Bearer
		// envelope even though the credential value is the configured
		// password rather than a JWT.
		value = "Bearer " + value
	}
	req.Header.Set(c.authHeader, value)
	return nil
}

// CredentialsReady validates that a required local password is available
// without returning it. A caller may provide an action-scoped password or use
// the pre-existing environment variable contract.
func (c *Client) CredentialsReady() error {
	if c.authHeader == "" || c.authEnv == "" || c.authOptional {
		return nil
	}
	if c.password != "" {
		return nil
	}
	if value, ok := os.LookupEnv(c.authEnv); !ok || value == "" {
		return fmt.Errorf("%s is not set", c.authEnv)
	}
	return nil
}

func validateNotebookLink(raw string) error {
	if raw == "" {
		return errors.New("Open Notebook manifest has no UI link template")
	}
	probe := strings.ReplaceAll(raw, "{{notebook_id|urlencode}}", "notebook%3Atest")
	u, err := url.Parse(probe)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("Open Notebook UI link must be an http(s) URL")
	}
	if !isLoopbackURL(u) {
		return errors.New("Open Notebook UI link must be loopback")
	}
	return nil
}

func isLoopbackURL(u *url.URL) bool {
	host := strings.Trim(u.Hostname(), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func decodeSource(body io.Reader) (Source, error) {
	var raw struct {
		ID       string `json:"id"`
		SourceID string `json:"source_id"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(body, maxResponseBody)).Decode(&raw); err != nil {
		return Source{}, fmt.Errorf("decode Open Notebook source: %w", err)
	}
	if raw.ID == "" {
		raw.ID = raw.SourceID
	}
	if raw.ID == "" {
		return Source{}, fmt.Errorf("Open Notebook response omitted source id")
	}
	if raw.Status == "" {
		raw.Status = "submitted"
	}
	return Source{ID: raw.ID, Status: raw.Status}, nil
}

func validateNotebookForm(fields map[string]string, notebookID string) error {
	if fields["type"] != "text" {
		return fmt.Errorf("type must be text")
	}
	if fields["content"] == "" {
		return fmt.Errorf("content is required")
	}
	for _, key := range []string{"embed", "async_processing", "delete_source"} {
		if value, ok := fields[key]; ok {
			switch value {
			case "true", "false", "1", "0", "yes", "no", "on", "off":
			default:
				return fmt.Errorf("%s must be a boolean string", key)
			}
		}
	}
	raw := fields["notebooks"]
	if raw == "" {
		return fmt.Errorf("notebooks is required")
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return fmt.Errorf("notebooks must be a JSON string array: %w", err)
	}
	if len(ids) != 1 || ids[0] != notebookID {
		return fmt.Errorf("notebooks must contain exactly the requested notebook id")
	}
	return nil
}

func statusAllowed(status int, expected []int) bool {
	if len(expected) == 0 {
		return status == http.StatusOK || status == http.StatusCreated
	}
	for _, want := range expected {
		if status == want {
			return true
		}
	}
	return false
}

func httpError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = resp.Status
	}
	return fmt.Errorf("Open Notebook returned %d: %s", resp.StatusCode, detail)
}

// authenticatedHTTPError intentionally omits a loopback service's response
// body. Some services echo request headers in diagnostics; retaining that
// body would turn an action-scoped password into a job-history secret.
func authenticatedHTTPError(resp *http.Response) error {
	return fmt.Errorf("Open Notebook returned %d", resp.StatusCode)
}
