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

const serverInstructions = `freelancer.com account automation. Start with freelancer_whoami to confirm the session.

Finding work: freelancer_projects_search (query, jobs, budget, project types), then freelancer_project_get for
the full brief and freelancer_project_bids to see the competition. freelancer_bid_quota shows how many bids are
left this month; freelancer_skills_search maps skill names to the job ids the search and profile tools need.

Bidding: freelancer_bid_place needs confirm=true because it spends quota and is visible to the client.
freelancer_bid_update edits a live proposal, freelancer_bid_action applies retract/highlight/sponsor as a
freelancer, or award/revoke/shortlist as the employer. freelancer_bids_list tracks what is outstanding.

Profile: freelancer_profile_get reads tagline, summary, hourly rate, and skills. freelancer_profile_update
writes tagline, summary (100 character minimum), and hourly rate. Skills are managed with
freelancer_profile_skills (list/set/add/remove). CV records live in freelancer_profile_cv
(experience, education, publication, certification).

Messaging: freelancer_threads_list then freelancer_messages_list, reply with freelancer_message_send,
clear badges with freelancer_thread_action action=read. freelancer_thread_new starts a conversation, but
Freelancer rejects cold messages with no shared project context.

Money: freelancer_balances, freelancer_invoices, freelancer_payout_accounts, freelancer_membership.
Milestones: freelancer_milestones_list, freelancer_milestone_requests_list, freelancer_milestone_request_create
to ask the client to fund work. freelancer_milestone_release moves escrow and needs confirm=true.

Anything not covered: freelancer_api_call with an explicit method, base, and path. The endpoint inventory in
docs/ lists the paths the web app itself uses.`
