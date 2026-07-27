// Package mcpserver exposes freelancer.com account tools over the Model Context
// Protocol on stdio, so an AI agent can read and act on the same account the
// CLI is logged into.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Protocol constants.
const (
	protocolVersion = "2024-11-05"
	jsonRPCVersion  = "2.0"
)

// JSON-RPC error codes used by the server.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// Transport reads and writes newline-delimited JSON-RPC messages.
type Transport struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex
}

// NewTransport wraps stdio streams.
func NewTransport(r io.Reader, w io.Writer) *Transport {
	return &Transport{reader: bufio.NewReaderSize(bufio.NewReader(r), 1<<20), writer: w}
}

// Read returns the next request. io.EOF signals a closed client.
func (t *Transport) Read() (*request, error) {
	for {
		line, err := t.reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			return nil, err
		}
		if len(trimSpace(line)) == 0 {
			if err != nil {
				return nil, err
			}
			continue
		}
		var req request
		if jsonErr := json.Unmarshal(line, &req); jsonErr != nil {
			return nil, &rpcError{Code: codeParseError, Message: "invalid JSON: " + jsonErr.Error()}
		}
		if req.JSONRPC != jsonRPCVersion {
			return nil, &rpcError{Code: codeInvalidRequest, Message: "unsupported jsonrpc version"}
		}
		return &req, nil
	}
}

// Write sends one message.
func (t *Transport) Write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	t.mu.Lock()
	defer t.mu.Unlock()
	_, err = t.writer.Write(data)
	return err
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}

// Serve runs the request loop until the transport closes or ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		req, err := s.transport.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			var rpcErr *rpcError
			if errors.As(err, &rpcErr) {
				_ = s.transport.Write(response{JSONRPC: jsonRPCVersion, Error: rpcErr})
				continue
			}
			return err
		}

		result, handleErr := s.handle(ctx, req)
		if req.ID == nil {
			// Notification: no response is expected.
			continue
		}
		resp := response{JSONRPC: jsonRPCVersion, ID: req.ID}
		if handleErr != nil {
			var rpcErr *rpcError
			if !errors.As(handleErr, &rpcErr) {
				rpcErr = &rpcError{Code: codeInternalError, Message: handleErr.Error()}
			}
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
		if err := s.transport.Write(resp); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req *request) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "freelancer-mcp",
				"version": s.version,
			},
			"instructions": serverInstructions,
		}, nil
	case "notifications/initialized", "initialized", "notifications/cancelled":
		return map[string]any{}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.toolSchemas()}, nil
	case "tools/call":
		return s.callTool(ctx, req.Params)
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown method " + req.Method}
	}
}

const serverInstructions = `freelancer.com account automation for a single logged-in account. This server acts as
that person on a live marketplace: bids are public, quota is small, and money moves. Read the limits below
before acting.

START HERE
freelancer_whoami confirms the session. freelancer_account_limits tells you what the account may bid on today:
bids left, the per-project USD ceiling, whether featured projects are reachable, verification and review
status, and a Blockers list. Call it before writing any proposal; skipping it wastes bids on projects the API
will refuse.

HARD LIMITS (server enforced, not preferences)
- Bid allowance is small and monthly. When it is spent, stop bidding and say so; it does not refill early.
- Projects worth 2500 USD or more require Verified by Freelancer (identity + payment). Unverified accounts get
  ProjectExceptionCodes.RESTRICTED_FROM_BIDDING_PREMIUM_VERIFIED. Do not retry, pick another project.
- Featured projects require 5 reviews, a paid membership, or verification. Same rule: do not retry.
- One bid per project. To change terms use freelancer_bid_update, not a second bid.
- Free accounts cap skills at 20, and profile_description must be at least 100 characters.

MONEY AND UNITS
- Every project carries its own currency. bid amount is in the PROJECT's currency, never USD. Convert with
  currency.exchange_rate before comparing value across projects, or you will read 12500 INR as a large budget.
- On hourly projects, bid amount is the hourly rate. period is always delivery days.
- Budget filters in freelancer_projects_search only apply when exactly one project_type is set, and they match
  the project's average price in the account currency.

FINDING WORK
freelancer_skills_search maps skill names to the job ids search and profile tools need. Then
freelancer_projects_search (sort_field=submitdate), freelancer_project_get for the full brief, and
freelancer_project_bids to size up the competition. Most listings already carry 50+ bids, so bid_stats.bid_count
and age are the signals that matter. Read the brief before proposing: many listings are out of scope for a
developer account, and some ask for work that breaks Freelancer's terms (cracking licensed code, fake accounts
or engagement, scraped personal data). Decline those and say why.

BIDDING
freelancer_bid_place needs confirm=true. The proposal must be the user's own offer: answer what the brief
actually asks, name the specific risks in that kind of build, and never invent experience, clients, or
certifications the profile does not show. Quote against the project's own currency and budget range. Track
outstanding work with freelancer_bids_list; freelancer_bid_action retract/highlight/sponsor as the freelancer,
or award/revoke/shortlist as the employer. retract, award, revoke, and deny need confirm=true.

PROFILE
freelancer_profile_get reads tagline, summary, hourly rate, and skills. freelancer_profile_update writes those
three fields only. freelancer_profile_skills manages skills (list/set/add/remove). freelancer_profile_cv covers
experience, education, publication, certification: list before adding, because these endpoints can write a row
even while returning an error, and duplicates are visible to clients. CV dates accept "YYYY-MM", "YYYY-MM-DD",
epoch seconds, or "present". Education needs a school_id from freelancer_schools_search.

MESSAGING
freelancer_threads_list, then freelancer_messages_list for history, freelancer_message_send to reply,
freelancer_thread_action action=read to clear badges. freelancer_thread_new only works where a shared context
exists; Freelancer rejects cold outreach.

MONEY TOOLS
freelancer_balances, freelancer_invoices, freelancer_payout_accounts, freelancer_membership.
Milestones: freelancer_milestones_list, freelancer_milestone_requests_list, freelancer_milestone_request_create
to ask a client to fund work. freelancer_milestone_release moves escrow, cannot be undone, and needs
confirm=true.

WHEN SOMETHING FAILS
Errors carry Freelancer's own code plus a hint on what to do next. A restriction is an answer, not a transport
error: report it and move on rather than retrying. If an endpoint is missing, freelancer_api_call takes an
explicit method, base (api or web), and path.

WHAT TO REPORT BACK
State what was actually done, what it cost (bids spent, money committed), and what is blocked with the reason.
If quota or verification stopped you short of the request, say so plainly instead of substituting easier work.`
