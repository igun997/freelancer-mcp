package freelancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Self is the trimmed identity returned by GET /users/0.1/self.
type Self struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	ChosenRole  string `json:"chosen_role"`
	Suspended   bool   `json:"suspended"`
	Closed      bool   `json:"closed"`
	Location    *struct {
		City    string `json:"city"`
		Country *struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"location"`
	PrimaryCurrency *Currency `json:"primary_currency"`
	Timezone        *struct {
		Timezone string  `json:"timezone"`
		Offset   float64 `json:"offset"`
	} `json:"timezone"`
	Status *struct {
		EmailVerified    bool `json:"email_verified"`
		PaymentVerified  bool `json:"payment_verified"`
		PhoneVerified    bool `json:"phone_verified"`
		IdentityVerified bool `json:"identity_verified"`
	} `json:"status"`
}

// Job is a skill tag.
type Job struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	SEOURL   string `json:"seo_url"`
	Category *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"category"`
}

// Currency is a Freelancer currency record.
type Currency struct {
	ID           int64   `json:"id"`
	Code         string  `json:"code"`
	Sign         string  `json:"sign"`
	Name         string  `json:"name"`
	ExchangeRate float64 `json:"exchange_rate"`
	Country      string  `json:"country"`
}

// Profile is the public-facing profile view. The `self` endpoint omits tagline,
// so profile reads go through /users/0.1/users.
type Profile struct {
	ID                 int64   `json:"id"`
	Username           string  `json:"username"`
	DisplayName        string  `json:"display_name"`
	Tagline            string  `json:"tagline"`
	ProfileDescription string  `json:"profile_description"`
	HourlyRate         float64 `json:"hourly_rate"`
	Jobs               []Job   `json:"jobs"`
	PrimaryLanguage    string  `json:"primary_language"`
	Role               string  `json:"role"`
	ChosenRole         string  `json:"chosen_role"`
	Location           *struct {
		City    string `json:"city"`
		Country *struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"location"`
	PrimaryCurrency *Currency `json:"primary_currency"`
}

// Self fetches the authenticated identity.
func (c *Client) Self(ctx context.Context, details ...string) (*Self, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("jobs", "true")
	query.Set("status", "true")
	for _, d := range details {
		query.Set(d, "true")
	}
	var out Self
	_, err := c.DoJSON(ctx, Request{Method: http.MethodGet, Path: "/users/0.1/self/", Query: query}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SelfRaw returns the raw self payload with caller-chosen detail flags.
func (c *Client) SelfRaw(ctx context.Context, query url.Values) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.API(ctx, http.MethodGet, "/users/0.1/self/", query, nil)
}

// Users looks up users by id with the detail flags the web app uses.
func (c *Client) Users(ctx context.Context, userIDs []int64, extra url.Values) (json.RawMessage, error) {
	if len(userIDs) == 0 {
		return nil, errors.New("no user ids")
	}
	query := idList("users", userIDs)
	query.Set("avatar", "true")
	query.Set("display_info", "true")
	query.Set("country_details", "true")
	query.Set("jobs", "true")
	query.Set("profile_description", "true")
	query.Set("status", "true")
	query.Set("qualification_details", "true")
	query = merge(query, extra)
	return c.API(ctx, http.MethodGet, "/users/0.1/users/", query, nil)
}

// Profile returns the full profile view of one user. Zero means "me".
func (c *Client) Profile(ctx context.Context, userID int64) (*Profile, error) {
	if userID == 0 {
		if err := c.requireSession(); err != nil {
			return nil, err
		}
		userID = c.UserID()
	}
	raw, err := c.Users(ctx, []int64{userID}, url.Values{"reputation": {"true"}})
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Users map[string]Profile `json:"users"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}
	profile, ok := wrapper.Users[strconv.FormatInt(userID, 10)]
	if !ok {
		return nil, fmt.Errorf("profile %d not found in response", userID)
	}
	return &profile, nil
}

// ProfileUpdate carries the three writable profile fields exposed by
// PUT /users/0.1/self/profile. Nil fields are left untouched.
type ProfileUpdate struct {
	Tagline            *string
	ProfileDescription *string
	HourlyRate         *float64
}

// Empty reports whether the update would change nothing.
func (u ProfileUpdate) Empty() bool {
	return u.Tagline == nil && u.ProfileDescription == nil && u.HourlyRate == nil
}

// UpdateProfile writes tagline, summary, and hourly rate.
//
// The endpoint only accepts these three keys; other account fields live behind
// /users/0.1/self/account, /self/primary_currency, and friends. Freelancer
// enforces a 100 character minimum on profile_description.
func (c *Client) UpdateProfile(ctx context.Context, update ProfileUpdate) error {
	if err := c.requireSession(); err != nil {
		return err
	}
	if update.Empty() {
		return errors.New("nothing to update: set tagline, description, or hourly rate")
	}
	payload := map[string]any{}
	if update.Tagline != nil {
		payload["tagline"] = *update.Tagline
	}
	if update.ProfileDescription != nil {
		payload["profile_description"] = *update.ProfileDescription
	}
	if update.HourlyRate != nil {
		payload["hourly_rate"] = *update.HourlyRate
	}
	_, err := c.API(ctx, http.MethodPut, "/users/0.1/self/profile", nil, payload)
	return err
}

// skillsForm builds the form body the jobs endpoints expect (`jobs[]=3&jobs[]=9`).
func skillsForm(jobIDs []int64) url.Values {
	form := url.Values{}
	for _, id := range jobIDs {
		form.Add("jobs[]", strconv.FormatInt(id, 10))
	}
	return form
}

// AddSkills appends skills to the profile.
func (c *Client) AddSkills(ctx context.Context, jobIDs []int64) error {
	return c.skills(ctx, http.MethodPost, jobIDs)
}

// SetSkills replaces the profile skill list. Freelancer requires at least one.
func (c *Client) SetSkills(ctx context.Context, jobIDs []int64) error {
	if len(jobIDs) == 0 {
		return errors.New("at least one skill id is required")
	}
	return c.skills(ctx, http.MethodPut, jobIDs)
}

// RemoveSkills drops skills from the profile.
func (c *Client) RemoveSkills(ctx context.Context, jobIDs []int64) error {
	return c.skills(ctx, http.MethodDelete, jobIDs)
}

func (c *Client) skills(ctx context.Context, method string, jobIDs []int64) error {
	if err := c.requireSession(); err != nil {
		return err
	}
	if len(jobIDs) == 0 {
		return errors.New("no skill ids given")
	}
	_, err := c.DoRaw(ctx, Request{
		Method: method,
		Path:   "/users/0.1/self/jobs/",
		Form:   skillsForm(jobIDs),
	})
	return err
}

// SetChosenRole switches the active role between "freelancer" and "employer".
func (c *Client) SetChosenRole(ctx context.Context, role string) error {
	if err := c.requireSession(); err != nil {
		return err
	}
	if role != "freelancer" && role != "employer" {
		return fmt.Errorf("role must be freelancer or employer, got %q", role)
	}
	_, err := c.API(ctx, http.MethodPut, "/users/0.1/self/account", nil, map[string]any{"chosen_role": role})
	return err
}

// SetPrimaryCurrency changes the account currency by id (1 = USD).
func (c *Client) SetPrimaryCurrency(ctx context.Context, currencyID int64) error {
	if err := c.requireSession(); err != nil {
		return err
	}
	_, err := c.API(ctx, http.MethodPut, "/users/0.1/self/primary_currency", nil,
		map[string]any{"currency_id": currencyID})
	return err
}

// UploadProfilePicture replaces the avatar. Crop values are in pixels on the
// uploaded image and are all required by the endpoint.
func (c *Client) UploadProfilePicture(ctx context.Context, name string, data []byte, x, y, cropW, cropH int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("x", strconv.Itoa(x))
	form.Set("y", strconv.Itoa(y))
	form.Set("cropW", strconv.Itoa(cropW))
	form.Set("cropH", strconv.Itoa(cropH))
	return c.DoRaw(ctx, Request{
		Method: http.MethodPost,
		Path:   "/users/0.1/self/profile_picture/",
		Form:   form,
		Files:  []FileUpload{{Field: "filedata", Name: name, Data: data}},
	})
}

// Reputation returns rating and job history for users in the given role.
func (c *Client) Reputation(ctx context.Context, userIDs []int64, role string) (json.RawMessage, error) {
	if len(userIDs) == 0 {
		return nil, errors.New("no user ids")
	}
	if role == "" {
		role = "freelancer"
	}
	query := idList("users", userIDs)
	query.Set("role", role)
	query.Set("job_history", "true")
	query.Set("project_stats", "true")
	query.Set("rehire_rates", "true")
	return c.API(ctx, http.MethodGet, "/users/0.1/reputations/", query, nil)
}

// FreelancerSearch filters the freelancer directory.
type FreelancerSearch struct {
	Query         string
	Jobs          []int64
	Countries     []string
	HourlyRateMin float64
	HourlyRateMax float64
	OnlineOnly    bool
	Limit         int
	Offset        int
}

// SearchFreelancers browses the public freelancer directory.
func (c *Client) SearchFreelancers(ctx context.Context, opts FreelancerSearch) (json.RawMessage, error) {
	query := url.Values{}
	if opts.Query != "" {
		query.Set("query", opts.Query)
	}
	query = merge(query, idList("jobs", opts.Jobs))
	for _, country := range opts.Countries {
		query.Add("countries[]", country)
	}
	if opts.HourlyRateMin > 0 {
		query.Set("hourly_rate_min", formatFloat(opts.HourlyRateMin))
	}
	if opts.HourlyRateMax > 0 {
		query.Set("hourly_rate_max", formatFloat(opts.HourlyRateMax))
	}
	if opts.OnlineOnly {
		query.Set("online_only", "true")
	}
	query.Set("limit", strconv.Itoa(limitOr(opts.Limit, 10)))
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	query.Set("avatar", "true")
	query.Set("jobs", "true")
	query.Set("reputation", "true")
	return c.API(ctx, http.MethodGet, "/users/0.1/users/directory/", query, nil)
}

// Portfolios lists portfolio items for users.
func (c *Client) Portfolios(ctx context.Context, userIDs []int64) (json.RawMessage, error) {
	if len(userIDs) == 0 {
		if err := c.requireSession(); err != nil {
			return nil, err
		}
		userIDs = []int64{c.UserID()}
	}
	query := idList("users", userIDs)
	query.Set("exclude_empty_items", "true")
	return c.API(ctx, http.MethodGet, "/users/0.1/portfolios/", query, nil)
}

// CVEntryKind selects which CV collection a write targets.
type CVEntryKind string

// CV collections exposed by the API.
const (
	CVExperience    CVEntryKind = "experiences"
	CVEducation     CVEntryKind = "educations"
	CVPublication   CVEntryKind = "publications"
	CVCertification CVEntryKind = "certifications"
)

// ParseCVEntryKind maps a user-facing name to a CV collection.
func ParseCVEntryKind(value string) (CVEntryKind, error) {
	switch value {
	case "experience", "experiences":
		return CVExperience, nil
	case "education", "educations":
		return CVEducation, nil
	case "publication", "publications":
		return CVPublication, nil
	case "certification", "certifications":
		return CVCertification, nil
	default:
		return "", fmt.Errorf("unknown cv section %q: use experience, education, publication, or certification", value)
	}
}

// RequiredFields documents the minimum payload per CV collection, taken from
// the API's own validation errors.
func (k CVEntryKind) RequiredFields() []string {
	switch k {
	case CVExperience:
		return []string{"title", "company", "start_date"}
	case CVEducation:
		return []string{"school_id or other_school_name", "country_code", "degree", "start_date"}
	case CVPublication:
		return []string{"title"}
	case CVCertification:
		return []string{"certificate", "awarded_date"}
	}
	return nil
}

// AddCVEntry creates a CV record (experience, education, publication,
// certification) on the authenticated profile.
func (c *Client) AddCVEntry(ctx context.Context, kind CVEntryKind, payload map[string]any) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty payload: %s needs %v", kind, kind.RequiredFields())
	}
	return c.API(ctx, http.MethodPost, "/users/0.1/"+string(kind), nil, payload)
}

// UpdateCVEntry edits one CV record.
func (c *Client) UpdateCVEntry(ctx context.Context, kind CVEntryKind, id int64, payload map[string]any) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.API(ctx, http.MethodPut, fmt.Sprintf("/users/0.1/%s/%d", kind, id), nil, payload)
}

// DeleteCVEntry removes one CV record.
func (c *Client) DeleteCVEntry(ctx context.Context, kind CVEntryKind, id int64) error {
	if err := c.requireSession(); err != nil {
		return err
	}
	_, err := c.API(ctx, http.MethodDelete, fmt.Sprintf("/users/0.1/%s/%d", kind, id), nil, nil)
	return err
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func limitOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
