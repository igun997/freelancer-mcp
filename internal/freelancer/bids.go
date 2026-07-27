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

// Bid is a proposal on a project.
type Bid struct {
	ID          int64   `json:"id"`
	BidderID    int64   `json:"bidder_id"`
	ProjectID   int64   `json:"project_id"`
	Amount      float64 `json:"amount"`
	Period      int     `json:"period"`
	Description string  `json:"description"`
	Retracted   bool    `json:"retracted"`
	AwardStatus string  `json:"award_status"`
	PaidStatus  string  `json:"paid_status"`
	Highlighted bool    `json:"highlighted"`
	Sponsored   float64 `json:"sponsored"`
	SubmitDate  int64   `json:"submitdate"`
	Score       float64 `json:"score"`
}

// BidList is the envelope of GET /projects/0.1/bids.
type BidList struct {
	Bids []Bid `json:"bids"`
}

// BidQuota is the freelancer's remaining monthly bid allowance.
type BidQuota struct {
	UserID          int64  `json:"userId"`
	BidsRemaining   int    `json:"bidsRemaining"`
	BidLimit        int    `json:"bidLimit"`
	BidRefreshTime  int64  `json:"bidRefreshTime"`
	BidRefreshRate  int    `json:"bidRefreshRate"`
	UnlimitedBids   bool   `json:"unlimitedBids"`
	BidsLastRefills string `json:"bidsLastRefilled"`
}

// BidListOptions filters bid queries.
type BidListOptions struct {
	Projects []int64
	Bidders  []int64
	Bids     []int64
	// FrontendStatuses are the web app's buckets: active, in_progress,
	// complete, awarded, pending, rejected, withdrawn.
	FrontendStatuses []string
	Limit            int
	Offset           int
}

// Bids lists bids. With no filters it returns the account's own bids.
func (c *Client) Bids(ctx context.Context, opts BidListOptions) (*BidList, error) {
	if len(opts.Projects) == 0 && len(opts.Bidders) == 0 && len(opts.Bids) == 0 {
		if err := c.requireSession(); err != nil {
			return nil, err
		}
		opts.Bidders = []int64{c.UserID()}
	}
	query := url.Values{}
	query = merge(query, idList("projects", opts.Projects))
	query = merge(query, idList("bidders", opts.Bidders))
	query = merge(query, idList("bids", opts.Bids))
	for _, status := range opts.FrontendStatuses {
		query.Add("frontend_bid_statuses[]", status)
	}
	query.Set("limit", strconv.Itoa(limitOr(opts.Limit, 20)))
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	var out BidList
	_, err := c.DoJSON(ctx, Request{Method: http.MethodGet, Path: "/projects/0.1/bids/", Query: query}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectBids lists competing bids on one project.
func (c *Client) ProjectBids(ctx context.Context, projectID int64, limit int) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	query.Set("user_details", "true")
	query.Set("user_avatar", "true")
	query.Set("user_reputation", "true")
	return c.API(ctx, http.MethodGet, fmt.Sprintf("/projects/0.1/projects/%d/bids/", projectID), query, nil)
}

// BidInput is a new proposal.
type BidInput struct {
	ProjectID int64
	Amount    float64
	// Period is the delivery time in days.
	Period int
	// MilestonePercentage is the upfront milestone share, defaults to 50.
	MilestonePercentage int
	Description         string
	// ProfileID targets one of the account's specialised profiles.
	ProfileID int64
}

// PlaceBid submits a proposal. This consumes one bid from the monthly quota and
// is visible to the client immediately.
func (c *Client) PlaceBid(ctx context.Context, in BidInput) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if in.ProjectID == 0 {
		return nil, errors.New("project_id is required")
	}
	if in.Amount <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	if in.Period <= 0 {
		return nil, errors.New("period (delivery days) must be greater than zero")
	}
	if in.Description == "" {
		return nil, errors.New("description is required")
	}
	if in.MilestonePercentage <= 0 {
		in.MilestonePercentage = 50
	}
	payload := map[string]any{
		"project_id":           in.ProjectID,
		"bidder_id":            c.UserID(),
		"amount":               in.Amount,
		"period":               in.Period,
		"milestone_percentage": in.MilestonePercentage,
		"description":          in.Description,
	}
	if in.ProfileID != 0 {
		payload["profile_id"] = in.ProfileID
	}
	return c.API(ctx, http.MethodPost, "/projects/0.1/bids/", nil, payload)
}

// Bid actions accepted by PUT /projects/0.1/bids/{id}.
const (
	// Freelancer-side actions.
	BidActionRetract   = "retract"
	BidActionHighlight = "highlight"
	BidActionSponsor   = "sponsor"
	BidActionAccept    = "accept"
	BidActionDeny      = "deny"
	// Employer-side actions.
	BidActionAward       = "award"
	BidActionRevoke      = "revoke"
	BidActionShortlist   = "shortlist"
	BidActionUnshortlist = "unshortlist"
	BidActionHide        = "hide"
	BidActionUnhide      = "unhide"
)

// BidActions lists every accepted action, for help text and tool schemas.
func BidActions() []string {
	return []string{
		BidActionRetract, BidActionHighlight, BidActionSponsor, BidActionAccept, BidActionDeny,
		BidActionAward, BidActionRevoke, BidActionShortlist, BidActionUnshortlist,
		BidActionHide, BidActionUnhide,
	}
}

// ValidBidAction reports whether action is accepted by the API.
func ValidBidAction(action string) bool {
	for _, candidate := range BidActions() {
		if candidate == action {
			return true
		}
	}
	return false
}

// BidAction applies an action to a bid. The action travels as a query
// parameter, matching the web app.
func (c *Client) BidAction(ctx context.Context, bidID int64, action string, extra url.Values) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if !ValidBidAction(action) {
		return nil, fmt.Errorf("unknown bid action %q: expected one of %v", action, BidActions())
	}
	query := url.Values{"action": {action}}
	query = merge(query, extra)
	return c.DoRaw(ctx, Request{
		Method: http.MethodPut,
		Path:   fmt.Sprintf("/projects/0.1/bids/%d/", bidID),
		Query:  query,
		Form:   url.Values{"action": {action}},
	})
}

// UpdateBid edits an existing proposal (amount, period, description).
func (c *Client) UpdateBid(ctx context.Context, bidID int64, in BidInput) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if in.Amount > 0 {
		payload["amount"] = in.Amount
	}
	if in.Period > 0 {
		payload["period"] = in.Period
	}
	if in.Description != "" {
		payload["description"] = in.Description
	}
	if in.MilestonePercentage > 0 {
		payload["milestone_percentage"] = in.MilestonePercentage
	}
	if len(payload) == 0 {
		return nil, errors.New("nothing to update: set amount, period, milestone percentage, or description")
	}
	return c.API(ctx, http.MethodPut, fmt.Sprintf("/projects/0.1/bids/%d/", bidID), nil, payload)
}

// BidQuota reads the remaining monthly bid allowance.
func (c *Client) BidQuota(ctx context.Context) (*BidQuota, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	raw, err := c.Ajax(ctx, "projects/getBidLimit.php", url.Values{
		"userId": {strconv.FormatInt(c.UserID(), 10)},
	})
	if err != nil {
		return nil, err
	}
	var out BidQuota
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode bid quota: %w", err)
	}
	return &out, nil
}

// ManageBids returns the "Manage" dashboard view of bids and awarded work.
// kind is one of ongoing, past, cancelled.
func (c *Client) ManageBids(ctx context.Context, kind string, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if kind == "" {
		kind = "ongoing"
	}
	return c.Ajax(ctx, "manage/bids.php", url.Values{
		"type":   {kind},
		"filter": {"All"},
		"limit":  {strconv.Itoa(limitOr(limit, 50))},
	})
}
