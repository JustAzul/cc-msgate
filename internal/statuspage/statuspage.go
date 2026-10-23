// Package statuspage reads an Atlassian Statuspage summary.json.
package statuspage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
)

// Off, as a source, switches the layer off.
const Off = "off"

// ErrOff is returned in place of an answer when the source is Off.
var ErrOff = errors.New("status page switched off")

var (
	// ErrUnreachable is what an error wraps when the source gave no page: the
	// request failed, or the source answered with a status other than 200.
	ErrUnreachable = errors.New("status page unreachable")
	// ErrUnreadable is what an error wraps when the source answered 200 with
	// something that is not a component listing.
	ErrUnreadable = errors.New("status page unreadable")
)

// maxSummaryBytes bounds the body read; a real summary is a few tens of KiB.
const maxSummaryBytes = 4 << 20

const majorOutage = "major_outage"

// MajorOutage reports whether any of the watched components is at
// major_outage on the summary.json at url. Every lesser status, and a
// component not watched, is not an outage. An error means the page could not be
// read (ErrOff when url is Off, else ErrUnreachable or ErrUnreadable); it says nothing about the components.
func MajorOutage(ctx context.Context, url string, watched ...string) (bool, error) {
	if url == Off {
		return false, ErrOff
	}
	components, err := fetchComponents(ctx, url)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(components, func(c component) bool {
		return c.Status == majorOutage && slices.Contains(watched, c.Name)
	}), nil
}

type component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// fetchComponents GETs the summary at url and returns its component listing.
func fetchComponents(ctx context.Context, url string) ([]component, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("status page request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the source answered %s", ErrUnreachable, resp.Status)
	}
	var doc struct {
		Components *[]component `json:"components"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSummaryBytes)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: the answer is not a summary: %w", ErrUnreadable, err)
	}
	if doc.Components == nil {
		return nil, fmt.Errorf("%w: the answer lists no components", ErrUnreadable)
	}
	return *doc.Components, nil
}
