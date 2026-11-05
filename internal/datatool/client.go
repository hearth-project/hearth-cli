package datatool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultPrefix is where the service serves its API.
const DefaultPrefix = "/v1/datatool/api"

// maxBody caps what is read from a response.
const maxBody = 8 << 20

// Client talks to one service. Token is sent as X-Datatool-Token when set.
type Client struct {
	BaseURL string
	Prefix  string
	Token   string
	HTTP    *http.Client
}

// New returns a client with a timeout. proxy false connects directly,
// ignoring HTTP_PROXY and friends (the service is usually internal).
func New(baseURL, prefix, token string, timeout time.Duration, proxy bool) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("transfer url must look like http://host[:port]")
	}
	if u.User != nil {
		return nil, errors.New("transfer url must not contain a user name or password (config.toml is not private; use token_secret)")
	}
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if !proxy {
		tr.Proxy = nil
	}
	return &Client{
		BaseURL: strings.TrimRight(u.String(), "/"),
		Prefix:  "/" + strings.Trim(prefix, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: timeout, Transport: tr},
	}, nil
}

// Error is a failure the service reported.
type Error struct {
	HTTPStatus int
	Code       int
	Msg        string
}

func (e *Error) Error() string {
	msg := e.Msg
	if msg == "" {
		msg = http.StatusText(e.HTTPStatus)
	}
	if e.Code != 0 {
		return fmt.Sprintf("transfer service: %s (code %d)", msg, e.Code)
	}
	return fmt.Sprintf("transfer service: %s (HTTP %d)", msg, e.HTTPStatus)
}

// envelope is the common wrapper: {"code":0,"msg":"ok","data":...}.
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.BaseURL + c.Prefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("X-Datatool-Token", c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("transfer service %s: %w", c.BaseURL, redactURL(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("transfer service: reading the response: %w", err)
	}
	var env envelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		if resp.StatusCode >= 300 {
			return &Error{HTTPStatus: resp.StatusCode}
		}
		return fmt.Errorf("transfer service: not a DataTool response (%s)", path)
	}
	if resp.StatusCode >= 300 || env.Code != 0 {
		return &Error{HTTPStatus: resp.StatusCode, Code: env.Code, Msg: env.Msg}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("transfer service: unexpected %s response: %w", path, err)
	}
	return nil
}

// redactURL drops the URL from net/http errors (it may carry credentials
// someone put into the configured address).
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Meta returns the storages, allowed directions, defaults and limits.
func (c *Client) Meta(ctx context.Context) (*Meta, error) {
	var m Meta
	if err := c.do(ctx, http.MethodGet, "/meta", nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateTask submits a copy. The service starts it at once.
func (c *Client) CreateTask(ctx context.Context, req CreateRequest) (*Task, error) {
	// The service answers {"task":{...}} or just {"id":"..."}.
	var out struct {
		Task *Task  `json:"task"`
		ID   string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/tasks", nil, req, &out); err != nil {
		return nil, err
	}
	if out.Task != nil && out.Task.ID != "" {
		return out.Task, nil
	}
	if out.ID != "" {
		return &Task{ID: out.ID, Name: req.Name, Src: req.Src, Dst: req.Dst, Options: req.Options, Status: StatusPending}, nil
	}
	return nil, errors.New("transfer service: the new task has no id")
}

// GetTask returns one task.
func (c *Client) GetTask(ctx context.Context, id string) (*Task, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid task id %q", id)
	}
	var t Task
	if err := c.do(ctx, http.MethodGet, "/tasks/"+url.PathEscape(id), nil, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks returns one page of tasks, newest first as the service orders them.
func (c *Client) ListTasks(ctx context.Context, q TaskQuery) ([]Task, error) {
	v := url.Values{}
	if q.Keyword != "" {
		v.Set("keyword", q.Keyword)
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}
	v.Set("page", strconv.Itoa(q.Page))
	v.Set("pageSize", strconv.Itoa(q.PageSize))
	var out struct {
		Items []Task `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/tasks", v, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// CancelTask asks the service to stop a task.
func (c *Client) CancelTask(ctx context.Context, id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid task id %q", id)
	}
	return c.do(ctx, http.MethodPost, "/tasks/"+url.PathEscape(id)+"/cancel", nil, nil, nil)
}

// validID accepts the ids the service makes (dt-20261105-3dbb92fd) and
// nothing that could change the request path.
func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
