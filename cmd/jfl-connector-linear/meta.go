package main

import (
	"encoding/json"
	"fmt"
)

// meta is what jfl keeps of an issue that Linear has no place for: the
// agent session of its Claim, and the Links that aren't Linear "blocks"
// relations. It is kept as the metadata of one attachment of the issue,
// with the URL metaURL, which Linear shows in the issue's sidebar (ADR
// 0013).
type meta struct {
	Claim *claimMeta        `json:"claim,omitempty"`
	Links map[string][]link `json:"links,omitempty"`
}

// claimMeta is a Claim jfl recorded: the session, and the id of the user
// it assigned.
type claimMeta struct {
	Session  string `json:"session"`
	Assignee string `json:"assignee"`
}

// metaURL is the URL of the attachment that keeps an issue's metadata.
// Linear keeps one attachment per URL on an issue; the .invalid domain
// (RFC 2606) never resolves, so it can't be taken for a real link.
const metaURL = "https://jigflow.invalid/meta"

// empty reports whether there is nothing to keep.
func (m meta) empty() bool { return m.Claim == nil && len(m.Links) == 0 }

// addLink adds a Link to the metadata.
func (m *meta) addLink(name string, l link) {
	if m.Links == nil {
		m.Links = map[string][]link{}
	}
	m.Links[name] = append(m.Links[name], l)
}

// subtitle is what the attachment shows in Linear.
func (m meta) subtitle() string {
	if m.Claim != nil {
		return fmt.Sprintf("Claimed by agent session %s", m.Claim.Session)
	}
	return "Links kept by JigFlow"
}

// metaOf returns the issue's metadata, and the id of the attachment that
// keeps it, if any.
func metaOf(i lnIssue) (meta, string) {
	for _, a := range i.Attachments.Nodes {
		if a.URL != metaURL {
			continue
		}
		var m meta
		if json.Unmarshal(a.Metadata, &m) != nil {
			return meta{}, a.ID
		}
		return m, a.ID
	}
	return meta{}, ""
}
