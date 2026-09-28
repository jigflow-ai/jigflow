package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// client talks to GitHub's REST API for one repository.
type client struct {
	api   string // the API's base URL, without a trailing slash
	token string
	repo  string // owner/name
	http  *http.Client
}

// apiError is GitHub answering a request with an error status, or not
// answering it.
type apiError struct {
	status     int // 0 when GitHub couldn't be reached
	message    string
	rateLimit  bool
	retryAfter int
}

func (e *apiError) Error() string { return e.message }

// ghIssue is an issue as GitHub's REST API shows it.
type ghIssue struct {
	ID     int64   `json:"id"`
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Body   *string `json:"body"`
	State  string  `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	PullRequest  *json.RawMessage `json:"pull_request"`
	Dependencies *struct {
		TotalBlockedBy int `json:"total_blocked_by"`
	} `json:"issue_dependencies_summary"`
}

// hasLabel reports whether the issue has the label.
func (i ghIssue) hasLabel(name string) bool {
	for _, l := range i.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

// body is the issue's body, empty when it has none.
func (i ghIssue) body() string {
	if i.Body == nil {
		return ""
	}
	return *i.Body
}

// logins are the logins of the issue's assignees.
func (i ghIssue) logins() []string {
	var out []string
	for _, a := range i.Assignees {
		out = append(out, a.Login)
	}
	return out
}

// do sends one request to the API, with the JSON of in as its body when in
// isn't nil, and decodes the response into out when out isn't nil. It
// returns the response's next page, from its Link header.
func (c *client) do(method, path string, in, out any) (next string, err error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return "", err
		}
		body = bytes.NewReader(data)
	}
	u := path
	if !strings.HasPrefix(u, "http") {
		u = c.api + path
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", &apiError{message: err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &apiError{message: err.Error()}
	}
	if resp.StatusCode >= 300 {
		return "", responseError(resp, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return "", fmt.Errorf("GitHub's response to %s %s is not the JSON expected: %v", method, path, err)
		}
	}
	return nextPage(resp.Header.Get("Link")), nil
}

// responseError is the error of a response with an error status.
func responseError(resp *http.Response, data []byte) *apiError {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &body)
	// jfl ends the message with its own full stop.
	e := &apiError{status: resp.StatusCode, message: strings.TrimSuffix(body.Message, ".")}
	if e.message == "" {
		e.message = resp.Status
	}
	h := resp.Header
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && (h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != "")) {
		e.rateLimit = true
		if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
			e.retryAfter = s
		} else if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			e.retryAfter = max(int(time.Until(time.Unix(reset, 0)).Seconds()), 1)
		}
	}
	return e
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// nextPage is the URL of the next page a Link header names, if any.
func nextPage(link string) string {
	if m := nextLink.FindStringSubmatch(link); m != nil {
		return m[1]
	}
	return ""
}

// repoPath is the API path of the repository, followed by the given
// segments, each escaped.
func (c *client) repoPath(segments ...string) string {
	p := "/repos/" + c.repo
	for _, s := range segments {
		p += "/" + url.PathEscape(s)
	}
	return p
}

// issues returns every issue with the label, when it isn't empty, open or
// closed, and no pull request.
func (c *client) issues(label string) ([]ghIssue, error) {
	q := url.Values{"state": {"all"}, "per_page": {"100"}}
	if label != "" {
		q.Set("labels", label)
	}
	var out []ghIssue
	for page := c.repoPath("issues") + "?" + q.Encode(); page != ""; {
		var batch []ghIssue
		next, err := c.do("GET", page, nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, i := range batch {
			if i.PullRequest == nil {
				out = append(out, i)
			}
		}
		page = next
	}
	return out, nil
}

// notFound reports whether err is GitHub having no such resource.
func notFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && (e.status == http.StatusNotFound || e.status == http.StatusGone)
}
