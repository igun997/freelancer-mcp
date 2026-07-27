package freelancer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// VerifiedBidCeilingUSD is the bid amount above which Freelancer requires
// "Verified by Freelancer" status. Bids over it fail with
// ProjectExceptionCodes.RESTRICTED_FROM_BIDDING_PREMIUM_VERIFIED.
const VerifiedBidCeilingUSD = 2500

// FeaturedReviewThreshold is the review count that unlocks featured projects
// without a paid membership or verification.
const FeaturedReviewThreshold = 5

// AccountLimits is everything an automated caller needs before it spends a bid:
// what the account may bid on, how many bids remain, and what is blocking the
// rest. Every field is read from the API; the thresholds are constants
// Freelancer enforces server side.
type AccountLimits struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`

	BidsRemaining int           `json:"bids_remaining"`
	BidLimit      int           `json:"bid_limit"`
	UnlimitedBids bool          `json:"unlimited_bids"`
	BidsRefillIn  string        `json:"bids_refill_in,omitempty"`
	refillWindow  time.Duration `json:"-"`

	EmailVerified      bool `json:"email_verified"`
	PhoneVerified      bool `json:"phone_verified"`
	PaymentVerified    bool `json:"payment_verified"`
	IdentityVerified   bool `json:"identity_verified"`
	FreelancerVerified bool `json:"freelancer_verified"`

	ReviewCount int     `json:"review_count"`
	Rating      float64 `json:"rating"`

	PaidMembership bool    `json:"paid_membership"`
	SkillCount     int     `json:"skill_count"`
	PortfolioItems int     `json:"portfolio_items"`
	HourlyRate     float64 `json:"hourly_rate"`

	// MaxBidUSD is the highest project value this account may bid on right now.
	MaxBidUSD float64 `json:"max_bid_usd"`
	// CanBidFeatured reports whether featured projects are reachable.
	CanBidFeatured bool `json:"can_bid_featured"`
	// Blockers lists what an agent should not attempt, in plain language.
	Blockers []string `json:"blockers,omitempty"`
	// Notes carries advice that is not a hard block.
	Notes []string `json:"notes,omitempty"`
}

// AccountLimits gathers quota, verification, and reputation into one answer, so
// a caller can decide whether a project is biddable before writing a proposal.
func (c *Client) AccountLimits(ctx context.Context) (*AccountLimits, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	self, err := c.Self(ctx)
	if err != nil {
		return nil, err
	}
	out := &AccountLimits{
		UserID:     self.ID,
		Username:   self.Username,
		MaxBidUSD:  VerifiedBidCeilingUSD,
		SkillCount: 0,
	}
	if self.Status != nil {
		out.EmailVerified = self.Status.EmailVerified
		out.PhoneVerified = self.Status.PhoneVerified
		out.PaymentVerified = self.Status.PaymentVerified
		out.IdentityVerified = self.Status.IdentityVerified
	}

	if quota, err := c.BidQuota(ctx); err == nil {
		out.BidsRemaining = quota.BidsRemaining
		out.BidLimit = quota.BidLimit
		out.UnlimitedBids = quota.UnlimitedBids
		if quota.BidRefreshTime > 0 {
			out.refillWindow = time.Duration(quota.BidRefreshTime) * time.Second
			out.BidsRefillIn = out.refillWindow.Round(time.Minute).String()
		}
	} else {
		out.Notes = append(out.Notes, "bid quota unavailable: "+err.Error())
	}

	if profile, err := c.Profile(ctx, self.ID); err == nil {
		out.SkillCount = len(profile.Jobs)
		out.HourlyRate = profile.HourlyRate
	}
	if raw, err := c.Reputation(ctx, []int64{self.ID}, "freelancer"); err == nil {
		out.ReviewCount, out.Rating = parseReputation(raw, self.ID)
	}
	if raw, err := c.Memberships(ctx); err == nil {
		var logs []map[string]any
		if json.Unmarshal(raw, &logs) == nil {
			out.PaidMembership = len(logs) > 0
		}
	}
	if raw, err := c.Portfolios(ctx, []int64{self.ID}); err == nil {
		var wrapper struct {
			Portfolios map[string]any `json:"portfolios"`
		}
		if json.Unmarshal(raw, &wrapper) == nil {
			out.PortfolioItems = len(wrapper.Portfolios)
		}
	}

	out.FreelancerVerified = out.IdentityVerified && out.PaymentVerified
	if out.FreelancerVerified {
		out.MaxBidUSD = 0 // no ceiling
	}
	out.CanBidFeatured = out.FreelancerVerified || out.PaidMembership || out.ReviewCount >= FeaturedReviewThreshold

	if !out.FreelancerVerified {
		out.Blockers = append(out.Blockers, fmt.Sprintf(
			"projects worth %d USD or more are closed to this account: Verified by Freelancer needs identity and payment verification (identity=%t, payment=%t)",
			VerifiedBidCeilingUSD, out.IdentityVerified, out.PaymentVerified))
	}
	if !out.CanBidFeatured {
		out.Blockers = append(out.Blockers, fmt.Sprintf(
			"featured projects are closed: they need %d reviews (have %d), a paid membership, or verification",
			FeaturedReviewThreshold, out.ReviewCount))
	}
	if !out.UnlimitedBids && out.BidsRemaining == 0 {
		out.Blockers = append(out.Blockers, "no bids left this cycle: stop bidding until the allowance refills")
	}
	if !out.UnlimitedBids && out.BidsRemaining > 0 && out.BidsRemaining <= 2 {
		out.Notes = append(out.Notes, fmt.Sprintf("only %d bids left: spend them on projects that match the profile closely", out.BidsRemaining))
	}
	if out.ReviewCount == 0 {
		out.Notes = append(out.Notes, "no reviews yet, so proposals win on specifics rather than reputation")
	}
	if out.PortfolioItems == 0 {
		out.Notes = append(out.Notes, "portfolio is empty, which clients notice when they ask for past work")
	}
	return out, nil
}

// CanBid reports whether a project of this USD value is biddable, and why not.
func (l *AccountLimits) CanBid(valueUSD float64, featured bool) (bool, string) {
	if l == nil {
		return false, "account limits unknown"
	}
	if !l.UnlimitedBids && l.BidsRemaining <= 0 {
		return false, "no bids left this cycle"
	}
	if featured && !l.CanBidFeatured {
		return false, "featured projects need reviews, a paid membership, or verification"
	}
	if l.MaxBidUSD > 0 && valueUSD >= l.MaxBidUSD {
		return false, fmt.Sprintf("projects at %d USD or more need Verified by Freelancer", VerifiedBidCeilingUSD)
	}
	return true, ""
}

func parseReputation(raw json.RawMessage, userID int64) (int, float64) {
	var wrapper map[string]struct {
		EntireHistory struct {
			Reviews int     `json:"reviews"`
			Overall float64 `json:"overall"`
		} `json:"entire_history"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return 0, 0
	}
	entry, ok := wrapper[fmt.Sprint(userID)]
	if !ok {
		return 0, 0
	}
	return entry.EntireHistory.Reviews, entry.EntireHistory.Overall
}
