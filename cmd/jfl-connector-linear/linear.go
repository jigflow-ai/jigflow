package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// client talks to Linear's GraphQL API.
type client struct {
	api  string // the GraphQL endpoint
	key  string // the API key, sent as Linear expects it
	http *http.Client
}

// apiError is Linear answering a request with an error, or not answering
// it.
type apiError struct {
	status     int    // the HTTP status; 0 when Linear couldn't be reached
	code       string // the GraphQL error's extensions code, if any
	message    string
	retryAfter int
}

func (e *apiError) Error() string { return e.message }

// do sends one GraphQL operation with the variables, and decodes its data
// into out.
func (c *client) do(query string, vars map[string]any, out any) error {
	data, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.api, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return &apiError{message: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &apiError{message: err.Error()}
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code                   string `json:"code"`
				UserPresentableMessage string `json:"userPresentableMessage"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	jsonErr := json.Unmarshal(body, &r)
	if len(r.Errors) > 0 {
		x := r.Errors[0]
		msg := x.Extensions.UserPresentableMessage
		if msg == "" {
			msg = x.Message
		}
		e := &apiError{status: resp.StatusCode, code: x.Extensions.Code, message: strings.TrimSuffix(msg, ".")}
		e.retryAfter = retryAfter(resp.Header)
		return e
	}
	if resp.StatusCode >= 300 {
		e := &apiError{status: resp.StatusCode, message: resp.Status}
		e.retryAfter = retryAfter(resp.Header)
		return e
	}
	if jsonErr != nil {
		return fmt.Errorf("Linear's response is not JSON: %v", jsonErr)
	}
	if out != nil {
		if err := json.Unmarshal(r.Data, out); err != nil {
			return fmt.Errorf("Linear's response is not the JSON expected: %v", err)
		}
	}
	return nil
}

// mutate sends a mutation of one field, whose payload reports its success,
// and decodes the payload into out when out isn't nil. A payload without
// success is Linear refusing to do what names.
func (c *client) mutate(query string, vars map[string]any, what string, out any) error {
	var r map[string]json.RawMessage
	if err := c.do(query, vars, &r); err != nil {
		return err
	}
	for _, payload := range r {
		var p struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("Linear's response is not the JSON expected: %v", err)
		}
		if !p.Success {
			return &failure{Kind: "invalid", Message: "Linear didn't " + what}
		}
		if out != nil {
			if err := json.Unmarshal(payload, out); err != nil {
				return fmt.Errorf("Linear's response is not the JSON expected: %v", err)
			}
		}
	}
	return nil
}

// retryAfter is the seconds until Linear lifts a rate limit, from the
// response's headers, or 0 when they don't say.
func retryAfter(h http.Header) int {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
		return s
	}
	for _, name := range []string{"X-RateLimit-Requests-Reset", "X-RateLimit-Complexity-Reset"} {
		// Linear gives the reset time in UTC epoch milliseconds.
		if ms, err := strconv.ParseInt(h.Get(name), 10, 64); err == nil {
			return max(int(math.Ceil(time.Until(time.UnixMilli(ms)).Seconds())), 1)
		}
	}
	return 0
}

// The shapes of Linear's objects the Connector reads.
type (
	lnIssue struct {
		ID          string  `json:"id"`
		Number      int     `json:"number"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		State       struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"state"`
		Assignee *lnUser `json:"assignee"`
		Labels   struct {
			Nodes []lnLabel `json:"nodes"`
		} `json:"labels"`
		InverseRelations struct {
			Nodes []struct {
				Type  string `json:"type"`
				Issue struct {
					Number int `json:"number"`
					Team   struct {
						Key string `json:"key"`
					} `json:"team"`
				} `json:"issue"`
			} `json:"nodes"`
		} `json:"inverseRelations"`
		Attachments struct {
			Nodes []lnAttachment `json:"nodes"`
		} `json:"attachments"`
	}
	lnUser struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	lnLabel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Team *struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	lnAttachment struct {
		ID       string          `json:"id"`
		URL      string          `json:"url"`
		Metadata json.RawMessage `json:"metadata"`
	}
	lnTeam struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		States struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"states"`
		DefaultIssueState *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"defaultIssueState"`
	}
)

// issueFields are the fields of an issue the Connector reads.
const issueFields = `id number title description
state { id name }
assignee { id displayName }
labels(first: 50) { nodes { id name } }
inverseRelations(first: 50) { nodes { type issue { number team { key } } } }
attachments(first: 10, filter: {url: {eq: "` + metaURL + `"}}) { nodes { id url metadata } }`

// hasLabel reports whether the issue has the label.
func (i lnIssue) hasLabel(name string) bool {
	for _, l := range i.Labels.Nodes {
		if l.Name == name {
			return true
		}
	}
	return false
}

// labelID is the id of the issue's label with the name, if it has it.
func (i lnIssue) labelID(name string) string {
	for _, l := range i.Labels.Nodes {
		if l.Name == name {
			return l.ID
		}
	}
	return ""
}

// description is the issue's description, empty when it has none.
func (i lnIssue) description() string {
	if i.Description == nil {
		return ""
	}
	return *i.Description
}

// issues returns the team's issues matching the filter, across pages.
func (c *client) issues(filter map[string]any) ([]lnIssue, error) {
	var out []lnIssue
	var after any
	for {
		var r struct {
			Issues struct {
				Nodes    []lnIssue `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		q := `query Issues($filter: IssueFilter, $after: String) {
  issues(filter: $filter, first: 50, after: $after) {
    nodes { ` + issueFields + ` }
    pageInfo { hasNextPage endCursor }
  }
}`
		if err := c.do(q, map[string]any{"filter": filter, "after": after}, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Issues.Nodes...)
		if !r.Issues.PageInfo.HasNextPage {
			return out, nil
		}
		after = r.Issues.PageInfo.EndCursor
	}
}
