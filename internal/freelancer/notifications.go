package freelancer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Notifications reads the newsfeed notification stream (bid awards, messages,
// project updates). The payload is an Elasticsearch-shaped envelope, so it is
// returned raw.
func (c *Client) Notifications(ctx context.Context, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limitOr(limit, 20))}}
	return c.API(ctx, http.MethodGet, "/newsfeed/0.1/newsfeed/notifications", query, nil)
}

// Newsfeed reads the activity feed.
func (c *Client) Newsfeed(ctx context.Context, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limitOr(limit, 20))}}
	return c.API(ctx, http.MethodGet, "/newsfeed/0.1/newsfeed", query, nil)
}

// SavedSearches lists saved project search filters.
func (c *Client) SavedSearches(ctx context.Context) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.API(ctx, http.MethodGet, "/users/0.1/search/saved_filters", url.Values{"type": {"project"}}, nil)
}

// NotificationPreferences reads email and push notification settings.
func (c *Client) NotificationPreferences(ctx context.Context) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.Ajax(ctx, "notifications/preferences.php", nil)
}
