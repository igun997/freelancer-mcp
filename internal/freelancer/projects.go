package freelancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ProjectSearch filters the active project feed.
type ProjectSearch struct {
	Query        string
	Jobs         []int64
	MinBudget    float64
	MaxBudget    float64
	Currencies   []int64
	ProjectTypes []string // fixed, hourly
	Languages    []string
	SortField    string // submitdate (default), bid_enddate, bid_count
	Limit        int
	Offset       int
	// FullDescription pulls whole briefs instead of previews.
	FullDescription bool
	// OnlyLocal restricts to local jobs near the account location.
	OnlyLocal bool
}

// Project is the trimmed project view used for listings.
type Project struct {
	ID          int64  `json:"id"`
	OwnerID     int64  `json:"owner_id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	SEOURL      string `json:"seo_url"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Preview     string `json:"preview_description"`
	SubmitDate  int64  `json:"submitdate"`
	BidStats    *struct {
		BidCount int     `json:"bid_count"`
		BidAvg   float64 `json:"bid_avg"`
	} `json:"bid_stats"`
	Budget *struct {
		Minimum float64 `json:"minimum"`
		Maximum float64 `json:"maximum"`
	} `json:"budget"`
	Currency *Currency `json:"currency"`
	Jobs     []Job     `json:"jobs"`
}

// URL returns the public project page.
func (p Project) URL(base string) string {
	if p.SEOURL == "" {
		return fmt.Sprintf("%s/projects/%d", strings.TrimSuffix(base, "/"), p.ID)
	}
	return fmt.Sprintf("%s/projects/%s", strings.TrimSuffix(base, "/"), p.SEOURL)
}

// ProjectList is the envelope of the project search endpoints.
type ProjectList struct {
	Projects   []Project `json:"projects"`
	TotalCount int       `json:"total_count"`
}

// SearchProjects queries the active project feed (the "Browse projects" page).
func (c *Client) SearchProjects(ctx context.Context, opts ProjectSearch) (*ProjectList, error) {
	query := url.Values{}
	if opts.Query != "" {
		query.Set("query", opts.Query)
	}
	query = merge(query, idList("jobs", opts.Jobs))
	query = merge(query, idList("project_upgrades", nil))
	for _, t := range opts.ProjectTypes {
		query.Add("project_types[]", t)
	}
	for _, l := range opts.Languages {
		query.Add("languages[]", l)
	}
	query = merge(query, idList("currencies", opts.Currencies))
	if opts.MinBudget > 0 {
		query.Set("min_avg_price", formatFloat(opts.MinBudget))
	}
	if opts.MaxBudget > 0 {
		query.Set("max_avg_price", formatFloat(opts.MaxBudget))
	}
	if opts.SortField != "" {
		query.Set("sort_field", opts.SortField)
	} else {
		query.Set("sort_field", "submitdate")
	}
	query.Set("limit", strconv.Itoa(limitOr(opts.Limit, 20)))
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	query.Set("job_details", "true")
	query.Set("upgrade_details", "true")
	query.Set("location_details", "true")
	if opts.FullDescription {
		query.Set("full_description", "true")
	}
	if opts.OnlyLocal {
		query.Set("only_local", "true")
	}

	var out ProjectList
	_, err := c.DoJSON(ctx, Request{Method: http.MethodGet, Path: "/projects/0.1/projects/active/", Query: query}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Project fetches one project with the full brief and owner details.
func (c *Client) Project(ctx context.Context, projectID int64) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("full_description", "true")
	query.Set("job_details", "true")
	query.Set("upgrade_details", "true")
	query.Set("attachment_details", "true")
	query.Set("qualification_details", "true")
	query.Set("selected_bids", "true")
	query.Set("user_details", "true")
	query.Set("user_avatar", "true")
	query.Set("user_country_details", "true")
	query.Set("user_employer_reputation", "true")
	query.Set("user_status", "true")
	return c.API(ctx, http.MethodGet, fmt.Sprintf("/projects/0.1/projects/%d/", projectID), query, nil)
}

// MyProjects lists projects the account posted as an employer.
func (c *Client) MyProjects(ctx context.Context, limit, offset int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := idList("owners", []int64{c.UserID()})
	query.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	query.Set("selected_bids", "true")
	query.Set("job_details", "true")
	return c.API(ctx, http.MethodGet, "/projects/0.1/projects/", query, nil)
}

// Jobs returns the skill catalogue. The list is large, so callers usually
// filter client side.
func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	query := url.Values{}
	query.Set("job_names_only", "true")
	var out []Job
	_, err := c.DoJSON(ctx, Request{Method: http.MethodGet, Path: "/projects/0.1/jobs/", Query: query}, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Currencies lists supported currencies.
func (c *Client) Currencies(ctx context.Context) ([]Currency, error) {
	var out struct {
		Currencies []Currency `json:"currencies"`
	}
	_, err := c.DoJSON(ctx, Request{Method: http.MethodGet, Path: "/projects/0.1/currencies/"}, &out)
	if err != nil {
		return nil, err
	}
	return out.Currencies, nil
}

// Reviews lists reviews written about a user (default: the account itself).
func (c *Client) Reviews(ctx context.Context, userID int64, role string, limit int) (json.RawMessage, error) {
	if userID == 0 {
		if err := c.requireSession(); err != nil {
			return nil, err
		}
		userID = c.UserID()
	}
	if role == "" {
		role = "freelancer"
	}
	query := idList("to_users", []int64{userID})
	query.Set("role", role)
	query.Set("project_details", "true")
	query.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	return c.API(ctx, http.MethodGet, "/projects/0.1/reviews/", query, nil)
}

// PostProject describes a project to publish as an employer.
type PostProject struct {
	Title       string
	Description string
	CurrencyID  int64
	JobIDs      []int64
	BudgetMin   float64
	BudgetMax   float64
	// Hourly turns the listing into an hourly project.
	Hourly bool
	// HourlyCommitment is hours per week, required for hourly projects.
	HourlyHours int
	// HourlyInterval is "week" or "month".
	HourlyInterval string
}

// PostProject publishes a project. Employer-side action: it spends the
// account's project quota and is visible publicly.
func (c *Client) PostProject(ctx context.Context, in PostProject) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if in.Title == "" || in.Description == "" {
		return nil, errors.New("title and description are required")
	}
	if len(in.JobIDs) == 0 {
		return nil, errors.New("at least one job (skill) id is required")
	}
	if in.CurrencyID == 0 {
		in.CurrencyID = 1
	}
	payload := map[string]any{
		"title":       in.Title,
		"description": in.Description,
		"currency":    map[string]any{"id": in.CurrencyID},
		"budget":      map[string]any{"minimum": in.BudgetMin, "maximum": in.BudgetMax},
		"jobs":        jobRefs(in.JobIDs),
	}
	if in.Hourly {
		interval := in.HourlyInterval
		if interval == "" {
			interval = "week"
		}
		payload["hourly_project_info"] = map[string]any{
			"commitment": map[string]any{"hours": in.HourlyHours, "interval": interval},
		}
	}
	return c.API(ctx, http.MethodPost, "/projects/0.1/projects/", nil, payload)
}

func jobRefs(ids []int64) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"id": id})
	}
	return out
}
