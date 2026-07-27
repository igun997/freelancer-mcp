# freelancer-mcp

Go CLI and MCP server for [freelancer.com](https://www.freelancer.com) accounts. Log in once, keep the session on disk, then browse projects, bid, message clients, edit your profile, and track milestones from a terminal or from an AI agent.

Two binaries:

| Binary | Purpose |
| --- | --- |
| `freelancer` | CLI for humans and scripts |
| `freelancer-mcp` | Model Context Protocol server on stdio for AI agents (45 tools) |

Works on freelancer.com and its regional domains (freelancer.co.id, freelancer.com.au, …) — they share one account system and one API.

## Requirements

- Go 1.23 or newer
- A freelancer.com account (this talks to the same endpoints as the web app)

## Install

```bash
go install github.com/igun997/freelancer-mcp/cmd/freelancer@latest
go install github.com/igun997/freelancer-mcp/cmd/freelancer-mcp@latest
```

From source:

```bash
git clone git@github.com:igun997/freelancer-mcp.git
cd freelancer-mcp
make build           # bin/freelancer and bin/freelancer-mcp, version stamped from git
make check           # gofmt + vet + tests
```

## Quick start

```bash
bin/freelancer login --user you@example.com     # prompts for the password
bin/freelancer whoami                           # confirm the session
bin/freelancer quota                            # bids left this month
bin/freelancer projects --query golang --limit 5
bin/freelancer messages threads --unread
```

Then point an AI agent at the same session:

```bash
bin/freelancer-mcp        # stdio MCP server
```

## Log in

```bash
freelancer login --user you@example.com                    # prompts for the password
freelancer login --user you@example.com --password-stdin < pass.txt
freelancer login --user you@example.com --otp 123456       # two-factor accounts
```

The chain mirrors the web app:

1. `GET www.freelancer.com/auth/device` returns a short-lived device token.
2. `POST www.freelancer.com/ajax-api/auth/login.php` returns `{token, user, userRole}`.
3. Every later call carries `freelancer-auth-v2: <user_id>;<token>`.

No cookies, no browser, no captcha for a normal password login. The auth hash is opaque and carries no expiry, so the CLI validates it by calling `/users/0.1/self/`.

Session state lands in `~/.config/freelancer/session-<profile>.json`, directory `0700`, file `0600`. Use `--profile NAME` for multiple accounts.

`--save-credentials` stores user and password in that file so the CLI and MCP server can re-login automatically when the token is rejected. The password is written in plain text; anyone who can read the file or a backup of it can log in as you. Remove it with `freelancer session forget-credentials`.

## CLI

```bash
# profile
freelancer profile show
freelancer profile update --tagline "Backend & API Engineer" --hourly-rate 25
freelancer profile update --summary-file bio.txt          # 100 character minimum
freelancer profile skills                                  # current skills
freelancer profile skills --add 248                        # Golang
freelancer profile skills --set 3,248,305                  # replace the list
freelancer profile avatar --file me.png --width 400 --height 400
freelancer profile cv --section experience --list
freelancer profile cv --section experience \
  --add '{"title":"Backend Engineer","company":"Acme","start_date":"2021-03","end_date":"present"}'
freelancer profile schools --country ID --query komputer     # school_id for education entries
freelancer profile cv --section education \
  --add '{"school_id":3997,"country_code":"ID","degree":"Bachelor of Information Systems","start_date":"2015-09","end_date":"2019-09"}'
freelancer profile role employer                            # switch active role
freelancer profile currency --id 1                          # USD
freelancer profile portfolio
freelancer profile reputation

# finding work
freelancer skills --query golang                            # skill name -> job id
freelancer projects --query golang --types fixed --min-budget 250 --limit 10
freelancer projects --jobs 248,305 --full
freelancer project get --id 40608147
freelancer project bids --id 40608147
freelancer freelancers --query golang --limit 5

# bidding
freelancer quota
freelancer limits                            # what you may bid on, and what blocks the rest
freelancer bid place --project 40608147 --amount 250 --days 7 --proposal-file pitch.md
freelancer bid update --id 165487951 --amount 300
freelancer bid retract --id 165487951
freelancer bids --status active,in_progress
freelancer bid award --id 165487951                         # employer side

# messaging
freelancer messages threads --unread
freelancer messages show --thread 123456 --limit 30
freelancer messages send --thread 123456 --message "On it, draft tonight"
freelancer messages send --thread 123456 --file build.zip
freelancer messages read --thread 123456
freelancer messages new --members 987654 --context-type project --context 40608147 --message "Hi"

# money and delivery
freelancer money balances
freelancer money invoices
freelancer money payout-accounts
freelancer money membership
freelancer milestones list
freelancer milestones requests
freelancer milestones request --project 40608147 --bid 165487951 --amount 125 --description "Phase 1"

# everything else
freelancer notifications --limit 10
freelancer notifications --saved-searches
freelancer reviews
freelancer currencies
freelancer api --query 'balance_details=true' /users/0.1/self/
freelancer api --method PUT --data '{"tagline":"Go dev"}' /users/0.1/self/profile
```

Add `--json` to any command for machine-readable output. Anything the CLI does not wrap is reachable with `freelancer api`; `docs/api-notes.md` lists the paths the web app itself uses.

## MCP server

```bash
freelancer-mcp                 # stdio, uses the default profile
freelancer-mcp --profile work
```

Client config, e.g. Claude Desktop or any MCP-capable agent:

```json
{
  "mcpServers": {
    "freelancer": {
      "command": "freelancer-mcp",
      "args": ["--profile", "default"]
    }
  }
}
```

Tool groups:

- **Account** — `freelancer_whoami`, `freelancer_profile_get`, `freelancer_profile_update`, `freelancer_profile_skills`, `freelancer_profile_cv`, `freelancer_schools_search`, `freelancer_profile_picture_upload`, `freelancer_portfolio_list`, `freelancer_account_settings`, `freelancer_reputation`, `freelancer_reviews`, `freelancer_currencies`, `freelancer_skills_search`, `freelancer_freelancers_search`
- **Work** — `freelancer_projects_search`, `freelancer_project_get`, `freelancer_project_bids`, `freelancer_projects_mine`, `freelancer_project_post`, `freelancer_bids_list`, `freelancer_bid_quota`, `freelancer_bid_place`, `freelancer_bid_update`, `freelancer_bid_action`, `freelancer_manage_bids`
- **Messaging** — `freelancer_threads_list`, `freelancer_messages_list`, `freelancer_messages_search`, `freelancer_message_send`, `freelancer_thread_action`, `freelancer_thread_new`, `freelancer_thread_attachments`, `freelancer_notifications`, `freelancer_saved_searches`
- **Money** — `freelancer_balances`, `freelancer_invoices`, `freelancer_payout_accounts`, `freelancer_membership`, `freelancer_milestones_list`, `freelancer_milestone_requests_list`, `freelancer_milestone_request_create`, `freelancer_milestone_request_action`, `freelancer_milestone_release`
- **Guardrails** — `freelancer_account_limits` (bids left, USD ceiling, featured access, blockers)
- **Escape hatch** — `freelancer_api_call`

Actions that cost money, spend quota, or cannot be undone require `confirm=true`: `freelancer_bid_place`, `freelancer_project_post`, `freelancer_milestone_release`, and `freelancer_bid_action` for retract, award, revoke, deny.

## Notes on the platform

- **Bid quota and ceilings.** Free accounts get a small monthly allowance, projects worth **$2,500 USD or more need Verified by Freelancer**, and featured projects need 5 reviews, a paid membership, or verification. `freelancer limits` reports all of it, including what is currently blocking you, so you do not spend bids on projects the API will refuse.
- **Currencies.** Each project has its own currency; a bid amount is in the project's currency, and on hourly projects it is an hourly rate. Convert with `currency.exchange_rate` before comparing budgets.
- **Profile writes.** `PUT /users/0.1/self/profile` accepts exactly three fields: `tagline`, `profile_description`, `hourly_rate`. Other account settings live behind separate endpoints (`self/account`, `self/primary_currency`, `self/operating_areas`).
- **CV dates.** Freelancer stores epoch seconds but reinterprets them in GMT+7, so a first-of-month date renders as the previous month. Pass `"YYYY-MM"`, `"YYYY-MM-DD"`, or `"present"` and the client anchors mid-month for you. Education entries need a `school_id` from `freelancer profile schools`; a plain school name is dropped by the API.
- **Cold messages.** Freelancer rejects new threads to users with no shared project context.
- **Realtime.** The web app uses a SockJS channel at `notifications.freelancer.com`; this client polls instead.

`docs/api-notes.md` documents the auth flow, calling conventions, and the endpoint map. `docs/_raw-endpoints.txt` and `docs/_raw-endpoint-payloads.txt` are the raw inventory extracted from the web app bundle, covering contests, groups, tasks, and enterprise routes that have no dedicated command yet.

## Environment

| Variable | Purpose |
| --- | --- |
| `FREELANCER_USER`, `FREELANCER_PASSWORD` | login credentials |
| `FREELANCER_PROFILE` | default profile name |
| `FREELANCER_SESSION_DIR` | session storage directory |
| `FREELANCER_API_BASE`, `FREELANCER_WEB_BASE` | endpoint overrides |
| `FREELANCER_USER_AGENT`, `FREELANCER_LANGUAGE` | request identity |
| `FREELANCER_TIMEOUT` | request timeout, e.g. `45s` |

## Development

```bash
make check     # gofmt, vet, tests
make test      # go test -race ./...
make build
```

Tests are hermetic: they run against `httptest` servers and a temporary session directory, so `go test ./...` never touches a real account.

## Disclaimer

Unofficial client. It uses the same endpoints as the freelancer.com web app and the documented `/api/**/0.1/**` routes. Use it with your own account and stay inside Freelancer's terms of service.
