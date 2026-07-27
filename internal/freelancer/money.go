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

// Balance is one currency balance on the account.
type Balance struct {
	Amount      float64   `json:"amount"`
	BonusAmount float64   `json:"bonus_amount"`
	Currency    *Currency `json:"currency"`
}

// Balances reads the account wallet, one entry per currency. The payload nests
// the list under result.account_balances.balances.
func (c *Client) Balances(ctx context.Context) ([]Balance, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	var out struct {
		AccountBalances struct {
			Balances []Balance `json:"balances"`
		} `json:"account_balances"`
		Balances []Balance `json:"balances"`
	}
	_, err := c.DoJSON(ctx, Request{
		Method: http.MethodGet,
		Path:   "/users/0.1/self/",
		Query:  url.Values{"balance_details": {"true"}},
	}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.AccountBalances.Balances) > 0 {
		return out.AccountBalances.Balances, nil
	}
	return out.Balances, nil
}

// Milestones lists escrow milestones for the account's projects.
func (c *Client) Milestones(ctx context.Context, projectIDs []int64, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := idList("projects", projectIDs)
	if len(projectIDs) == 0 {
		query = merge(query, idList("bidders", []int64{c.UserID()}))
	}
	query.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	query.Set("user_details", "true")
	return c.API(ctx, http.MethodGet, "/projects/0.1/milestones/", query, nil)
}

// MilestoneRequests lists milestone (payment) requests.
func (c *Client) MilestoneRequests(ctx context.Context, projectIDs []int64, statuses []string, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := idList("projects", projectIDs)
	if len(projectIDs) == 0 {
		query = merge(query, idList("bidders", []int64{c.UserID()}))
	}
	for _, status := range statuses {
		query.Add("statuses[]", status)
	}
	query.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	return c.API(ctx, http.MethodGet, "/projects/0.1/milestone_requests/", query, nil)
}

// MilestoneRequestInput asks the client to fund a milestone.
type MilestoneRequestInput struct {
	ProjectID      int64
	BidID          int64
	Amount         float64
	Description    string
	InitialPayment bool
}

// RequestMilestone asks the employer to fund work. Freelancer-side action.
func (c *Client) RequestMilestone(ctx context.Context, in MilestoneRequestInput) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if in.ProjectID == 0 || in.BidID == 0 {
		return nil, errors.New("project_id and bid_id are required")
	}
	if in.Amount <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	payload := map[string]any{
		"project_id":         in.ProjectID,
		"bid_id":             in.BidID,
		"amount":             in.Amount,
		"description":        in.Description,
		"is_initial_payment": in.InitialPayment,
	}
	return c.API(ctx, http.MethodPost, "/projects/0.1/milestone_requests/", nil, payload)
}

// Milestone request actions accepted by PUT /projects/0.1/milestone_requests/{id}.
const (
	MilestoneRequestAccept  = "accept"
	MilestoneRequestReject  = "reject"
	MilestoneRequestDelete  = "delete"
	MilestoneRequestRelease = "release"
)

// MilestoneRequestAction applies an action to a milestone request.
func (c *Client) MilestoneRequestAction(ctx context.Context, requestID int64, action string) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	switch action {
	case MilestoneRequestAccept, MilestoneRequestReject, MilestoneRequestDelete, MilestoneRequestRelease:
	default:
		return nil, fmt.Errorf("unknown milestone request action %q", action)
	}
	return c.API(ctx, http.MethodPut,
		fmt.Sprintf("/projects/0.1/milestone_requests/%d/", requestID),
		url.Values{"action": {action}}, map[string]any{"action": action})
}

// ReleaseMilestone releases escrow to the freelancer. Employer-side action and
// irreversible once the funds move.
func (c *Client) ReleaseMilestone(ctx context.Context, milestoneID int64, amount float64) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	payload := map[string]any{"action": "release"}
	if amount > 0 {
		payload["amount"] = amount
	}
	return c.API(ctx, http.MethodPut, fmt.Sprintf("/projects/0.1/milestones/%d/", milestoneID), nil, payload)
}

// Invoices lists hourly-project invoices.
func (c *Client) Invoices(ctx context.Context, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limitOr(limit, 20))}}
	return c.API(ctx, http.MethodGet, "/projects/0.1/invoices/", query, nil)
}

// PayoutAccounts lists withdrawal destinations configured on the account.
func (c *Client) PayoutAccounts(ctx context.Context) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.API(ctx, http.MethodGet, "/payments/0.1/payout_accounts/", nil, nil)
}

// Memberships lists membership (Plus, Professional, Premier) history.
func (c *Client) Memberships(ctx context.Context) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Add("statuses[]", "active")
	query.Add("statuses[]", "pending")
	query.Set("package_details", "true")
	query.Set("price_details", "true")
	query.Set("period_details", "true")
	return c.API(ctx, http.MethodGet, "/memberships/0.1/history_logs/", query, nil)
}
