package main

import (
	"encoding/json"
	"strings"
)

// meta is what jfl keeps of an issue that GitHub has no place for: the
// agent session of its Claim, and the Links that aren't GitHub
// dependencies. It is kept at the end of the issue's body, in an HTML
// comment GitHub doesn't render, and the body the Connector reports leaves
// it out.
type meta struct {
	Claim *claimMeta        `json:"claim,omitempty"`
	Links map[string][]link `json:"links,omitempty"`
}

// claimMeta is a Claim jfl recorded: the session, and whom it assigned.
type claimMeta struct {
	Session  string `json:"session"`
	Assignee string `json:"assignee"`
}

const (
	metaOpen  = "<!-- jigflow: "
	metaClose = " -->"
)

// splitBody splits an issue's body into its text and its metadata.
func splitBody(body string) (string, meta) {
	var m meta
	i := strings.LastIndex(body, metaOpen)
	if i < 0 {
		return body, m
	}
	rest := strings.TrimRight(body[i+len(metaOpen):], " \r\n")
	data, ok := strings.CutSuffix(rest, metaClose)
	if !ok || json.Unmarshal([]byte(data), &m) != nil {
		return body, meta{}
	}
	text := strings.TrimRight(body[:i], "\r\n")
	return text, m
}

// joinBody is an issue's body with the text and the metadata.
func joinBody(text string, m meta) string {
	if m.Claim == nil && len(m.Links) == 0 {
		return text
	}
	data, _ := json.Marshal(m)
	c := metaOpen + string(data) + metaClose
	if text == "" {
		return c
	}
	return text + "\n\n" + c
}

// addLink adds a Link to the metadata.
func (m *meta) addLink(name string, l link) {
	if m.Links == nil {
		m.Links = map[string][]link{}
	}
	m.Links[name] = append(m.Links[name], l)
}
