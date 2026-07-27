package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, input string) (*Server, *bytes.Buffer) {
	t.Helper()
	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	out := &bytes.Buffer{}
	server, err := New(strings.NewReader(input), out, Options{Profile: "test", Version: "test"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server, out
}

func decodeResponses(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var messages []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		messages = append(messages, msg)
	}
	return messages
}

func TestInitializeAndToolsList(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	messages := decodeResponses(t, out)
	if len(messages) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(messages))
	}

	initResult := messages[0]["result"].(map[string]any)
	if initResult["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", initResult["protocolVersion"])
	}
	if info := initResult["serverInfo"].(map[string]any); info["name"] != "freelancer-mcp" {
		t.Errorf("serverInfo = %v", info)
	}

	listResult := messages[1]["result"].(map[string]any)
	toolList := listResult["tools"].([]any)
	if len(toolList) < 30 {
		t.Fatalf("expected the full tool set, got %d", len(toolList))
	}
	names := map[string]bool{}
	for _, entry := range toolList {
		tool := entry.(map[string]any)
		name := tool["name"].(string)
		if names[name] {
			t.Errorf("duplicate tool %s", name)
		}
		names[name] = true
		if tool["description"] == "" {
			t.Errorf("tool %s has no description", name)
		}
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			t.Errorf("tool %s has no input schema", name)
		}
	}
	for _, required := range []string{
		"freelancer_whoami", "freelancer_profile_update", "freelancer_projects_search",
		"freelancer_bid_place", "freelancer_message_send", "freelancer_api_call",
	} {
		if !names[required] {
			t.Errorf("missing tool %s", required)
		}
	}
}

func TestUnknownMethodAndTool(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"does/not/exist"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"freelancer_nope","arguments":{}}}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	messages := decodeResponses(t, out)
	for _, msg := range messages {
		rpcErr, ok := msg["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected an error response, got %v", msg)
		}
		if rpcErr["code"].(float64) != codeMethodNotFound {
			t.Errorf("code = %v, want %d", rpcErr["code"], codeMethodNotFound)
		}
	}
}

func TestToolCallWithoutSessionReportsToolError(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"freelancer_bid_quota","arguments":{}}}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	messages := decodeResponses(t, out)
	result := messages[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected a tool error, got %v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), "no active session") {
		t.Errorf("unexpected message %v", content["text"])
	}
}

func TestWhoamiWorksWithoutSession(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"freelancer_whoami","arguments":{}}}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	messages := decodeResponses(t, out)
	result := messages[0]["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("whoami must not fail without a session: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "session_file") {
		t.Errorf("payload missing session_file: %s", text)
	}
}

func TestNotificationsGetNoResponse(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":9,"method":"ping"}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	messages := decodeResponses(t, out)
	if len(messages) != 1 || messages[0]["id"].(float64) != 9 {
		t.Fatalf("notifications must not produce a response: %v", messages)
	}
}

func TestArgumentCoercion(t *testing.T) {
	args := map[string]any{
		"limit":       float64(12),
		"limit_text":  "34",
		"amount":      float64(25.5),
		"confirm":     "true",
		"job_ids":     []any{float64(3), "248"},
		"job_csv":     "3, 248",
		"folders":     []any{"inbox", " sent "},
		"folders_csv": "inbox, sent",
	}
	if got := argInt(args, "limit", 0); got != 12 {
		t.Errorf("argInt = %d", got)
	}
	if got := argInt(args, "limit_text", 0); got != 34 {
		t.Errorf("argInt from string = %d", got)
	}
	if got := argInt(args, "missing", 7); got != 7 {
		t.Errorf("argInt fallback = %d", got)
	}
	if got := argFloat(args, "amount", 0); got != 25.5 {
		t.Errorf("argFloat = %v", got)
	}
	if !argBool(args, "confirm") {
		t.Error("argBool should parse the string true")
	}
	if got := argInt64Slice(args, "job_ids"); len(got) != 2 || got[1] != 248 {
		t.Errorf("argInt64Slice = %v", got)
	}
	if got := argInt64Slice(args, "job_csv"); len(got) != 2 || got[0] != 3 {
		t.Errorf("argInt64Slice csv = %v", got)
	}
	if got := argStringSlice(args, "folders"); len(got) != 2 || got[1] != "sent" {
		t.Errorf("argStringSlice = %v", got)
	}
	if got := argStringSlice(args, "folders_csv"); len(got) != 2 || got[1] != "sent" {
		t.Errorf("argStringSlice csv = %v", got)
	}
}

func TestToValuesEncodesArrays(t *testing.T) {
	values := toValues(map[string]any{
		"jobs[]": []any{float64(3), float64(248)},
		"limit":  float64(5),
		"flag":   true,
		"text":   "hello",
	})
	if got := values["jobs[]"]; len(got) != 2 || got[0] != "3" {
		t.Errorf("jobs[] = %v", got)
	}
	if values.Get("limit") != "5" {
		t.Errorf("limit = %q", values.Get("limit"))
	}
	if values.Get("flag") != "true" || values.Get("text") != "hello" {
		t.Errorf("unexpected values %v", values)
	}
}

func TestDestructiveToolsRequireConfirm(t *testing.T) {
	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	server, _ := newTestServer(t, "")
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"freelancer_bid_place", map[string]any{"project_id": 1, "amount": 10.0, "period": 3, "description": "x"}},
		{"freelancer_bid_action", map[string]any{"bid_id": 1, "action": "retract"}},
		{"freelancer_milestone_release", map[string]any{"milestone_id": 1}},
		{"freelancer_project_post", map[string]any{"title": "t", "description": "d", "job_ids": []any{3.0}}},
	} {
		var found bool
		for _, tool := range tools() {
			if tool.Name != tc.name {
				continue
			}
			found = true
			if _, err := tool.Handler(ctx, server, tc.args); err == nil {
				t.Errorf("%s should refuse to run without confirm=true", tc.name)
			}
		}
		if !found {
			t.Errorf("tool %s not registered", tc.name)
		}
	}
}

func TestInstructionsCarryTheHardLimits(t *testing.T) {
	// An agent driving this server has no other source for the marketplace rules
	// that silently reject work, so initialize must state them.
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
`
	server, out := newTestServer(t, input)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	instructions := decodeResponses(t, out)[0]["result"].(map[string]any)["instructions"].(string)
	for _, required := range []string{
		"freelancer_account_limits", // check limits before proposing
		"2500",                      // verified-only ceiling
		"Featured",                  // featured restriction
		"confirm=true",              // gates on destructive tools
		"PROJECT's currency",        // bid amount units
		"hourly rate",               // hourly semantics
		"delivery days",             // period semantics
		"do not retry",              // restrictions are answers, not transport errors
		"terms",                     // decline ToS-breaking briefs
		"never invent experience",   // no fabricated credentials
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("instructions missing %q", required)
		}
	}
}

func TestAccountLimitsToolIsRegisteredAndSessionGated(t *testing.T) {
	var found bool
	for _, tool := range tools() {
		if tool.Name != "freelancer_account_limits" {
			continue
		}
		found = true
		if tool.SkipSession {
			t.Error("account limits needs a session")
		}
		if !strings.Contains(tool.Description, "before writing any proposal") {
			t.Errorf("description should tell the agent when to call it: %q", tool.Description)
		}
	}
	if !found {
		t.Fatal("freelancer_account_limits not registered")
	}
}

func TestBidPlaceDescriptionExplainsUnits(t *testing.T) {
	for _, tool := range tools() {
		if tool.Name != "freelancer_bid_place" {
			continue
		}
		for _, required := range []string{"PROJECT's currency", "hourly rate", "days", "2500", "confirm"} {
			if !strings.Contains(tool.Description, required) {
				t.Errorf("bid_place description missing %q", required)
			}
		}
	}
}
