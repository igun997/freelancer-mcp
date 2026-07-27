# freelancer.com API notes

How this client talks to freelancer.com, captured from the production web app
(`main.*.js` bundle, `authConfig` / `freelancerHttpConfig`) and verified against a
live account. The official OAuth API at developers.freelancer.com serves the same
`/api/**/0.1/**` routes, so anything documented there applies here too — only the
auth header differs.

## Auth

Two calls, no browser and no captcha needed for a normal password login:

```bash
# 1. device token (short lived JWT, fingerprints the client)
curl -s 'https://www.freelancer.com/auth/device' -H 'accept: application/json'
# -> {"status":"success","result":{"token":"eyJ…"}}

# 2. login (form encoded)
curl -s 'https://www.freelancer.com/ajax-api/auth/login.php?compact=true&new_errors=true&new_pools=true' \
  -H 'content-type: application/x-www-form-urlencoded;charset=UTF-8' \
  --data-urlencode 'user=you@example.com' \
  --data-urlencode 'password=…' \
  --data-urlencode 'device_token=eyJ…' \
  --data 'captcha=&v3Captcha='
# -> {"status":"success","result":{"token":"chc37y…=","user":26605882,"userRole":"freelancer"}}
```

The returned `token` is an opaque auth hash with no expiry claim. Every
subsequent request carries it in a single header, `<user_id>;<token>`:

```
freelancer-auth-v2: 26605882;chc37y…=
```

No cookies are required. The web app also stores the same pair in the
`GETAFREE_AUTH_HASH_V2` and `GETAFREE_USER_ID` cookies, which is only relevant if
you want to hand a session to a browser.

Notes:

- The login response sets no cookies; the SPA writes them from the JSON body.
- Two-factor accounts need an extra `otp` field (`freelancer login --otp`).
- reCAPTCHA fields exist but stay empty for password logins. A blocked login
  returns HTTP 403 with an empty body; retrying from a clean client usually works.
- Sessions are additive: logging in from the CLI does not invalidate the browser.

## Bases

| Base | Purpose |
| --- | --- |
| `https://www.freelancer.com/api` | versioned REST API, `/{namespace}/0.1/...` |
| `https://www.freelancer.com` | `/auth/device` and every `/ajax-api/...` legacy endpoint |

Envelope for both:

```json
{"status":"success","result":{…},"request_id":"…"}
{"status":"error","message":"…","error_code":"ProjectExceptionCodes.…","request_id":"…"}
```

Errors arrive with a 4xx status *and* sometimes with HTTP 200, so the client
checks `status` as well as the status code. 401 maps to `ErrUnauthorized`, which
triggers one re-login when credentials were saved.

## Conventions that bite

- **Trailing slashes matter.** `PUT /users/0.1/self/profile` writes;
  `PUT /users/0.1/self/profile/` answers 200 and changes nothing.
- **Array parameters** use PHP style: `?users[]=1&users[]=2`, `jobs[]=3`.
- **Some writes are form encoded, not JSON.** Skills (`self/jobs`), messages, and
  thread creation are `application/x-www-form-urlencoded` or multipart; bids,
  profile, and milestones are JSON.
- **Actions travel as `?action=`** on PUT: `bids/{id}/?action=award`.
- **Detail flags are opt-in.** Fields come back `null` unless you ask
  (`jobs=true`, `full_description=true`, `user_details=true`, …).
- `GET /users/0.1/self/` never returns `tagline`; read it from
  `GET /users/0.1/users/?users[]=<id>`.

## Endpoints this client uses

### Identity and profile

| Method | Path | Notes |
| --- | --- | --- |
| GET | `users/0.1/self/` | identity; detail flags `jobs`, `status`, `balance_details` |
| GET | `users/0.1/users/` | full profile view incl. `tagline`, `hourly_rate` |
| PUT | `users/0.1/self/profile` | **only** `tagline`, `profile_description`, `hourly_rate` |
| POST/PUT/DELETE | `users/0.1/self/jobs/` | skills, form body `jobs[]=3&jobs[]=248`; PUT needs ≥1 |
| PUT | `users/0.1/self/account` | `chosen_role`, `directory_follow_preferences`, `mfa_preferences` |
| PUT | `users/0.1/self/primary_currency` | `{"currency_id":1}` |
| PUT | `users/0.1/self/operating_areas` | `{"user_id":…,"operating_area_ids":[…]}` |
| POST | `users/0.1/self/profile_picture/` | multipart `filedata` + `x`, `y`, `cropW`, `cropH` |
| POST | `users/0.1/self/cover_image/` | multipart `filedata` + `x`, `y`, `crop_width`, `crop_height`, `profile_type` |
| GET | `users/0.1/reputations/` | ratings, `job_history`, `project_stats` |
| GET | `users/0.1/portfolios/` | portfolio items |
| GET | `users/0.1/users/directory/` | freelancer search |
| POST/PUT/DELETE | `users/0.1/experiences`, `educations`, `publications`, `certifications` | CV records |
| PUT | `users/0.1/profiles` | specialised profiles: `profile_name`, `tagline`, `description`, `hourly_rate`, `skill_ids` |

Validation learned from the API itself:

- `profile_description` must be ≥ 100 characters (`UserExceptionCodes.PROFILE_DESCRIPTION_TOO_SHORT`).
- `experiences` needs `title`, `company`, `start_date`.
- `educations` needs a school, `country_code`, `degree`, `start_date`.
- `publications` needs `title`; `certifications` needs `certificate` and `awarded_date`.
- `POST` with an empty body can still create a blank row (`educations` does), so
  validate before calling.

### Projects and bids

| Method | Path | Notes |
| --- | --- | --- |
| GET | `projects/0.1/projects/active` | the browse feed: `query`, `jobs[]`, `project_types[]`, `min_avg_price`, `sort_field` |
| GET | `projects/0.1/projects/{id}/` | full brief |
| GET | `projects/0.1/projects/` | filter by `owners[]` for your own postings |
| POST | `projects/0.1/projects/` | post a project (employer) |
| GET | `projects/0.1/projects/{id}/bids/` | competing bids |
| GET | `projects/0.1/bids` | `bidders[]`, `projects[]`, `frontend_bid_statuses[]` |
| POST | `projects/0.1/bids/` | `{project_id, bidder_id, amount, period, milestone_percentage, description, profile_id}` |
| PUT | `projects/0.1/bids/{id}/` | `?action=` retract, highlight, sponsor, accept, deny (freelancer) / award, revoke, shortlist, unshortlist, hide, unhide (employer) |
| GET | `projects/0.1/jobs/` | skill catalogue |
| GET | `projects/0.1/currencies` | currencies |
| GET | `projects/0.1/reviews/` | reviews, filter with `to_users[]` |
| GET | `ajax-api/projects/getBidLimit.php?userId=` | monthly bid quota |
| GET | `ajax-api/manage/bids.php?type=ongoing` | manage dashboard |

Bid restrictions surface as business errors, e.g.
`ProjectExceptionCodes.RESTRICTED_FROM_BIDDING_ON_FEATURED` ("you need 5 reviews,
a paid membership, or verification"). Check `getBidLimit.php` first.

### Messaging

| Method | Path | Notes |
| --- | --- | --- |
| GET | `messages/0.1/threads` | `folders[]`, `is_read=false`, `last_message=true`, `unread_count=true` |
| GET | `messages/0.1/messages` | requires `threads[]` or `messages[]` |
| GET | `messages/0.1/messages/search` | in-thread search |
| POST | `messages/0.1/threads/` | form: `members[]`, `context_type`, `context`, `thread_type`, `message`, `source=21` |
| POST | `messages/0.1/threads/{id}/messages_new/` | form/multipart: `message`, `client_message_id`, `files[]` |
| PUT | `messages/0.1/threads/` | `{"threads":[…],"action":"read"}`; also mute/unmute, block/unblock, star/unstar |
| GET | `messages/0.1/threads/{id}/attachments` | shared files |

### Money

| Method | Path | Notes |
| --- | --- | --- |
| GET | `users/0.1/self/?balance_details=true` | wallet under `result.account_balances.balances` |
| GET | `projects/0.1/milestones/` | escrow milestones |
| POST | `projects/0.1/milestone_requests/` | `{project_id, bid_id, amount, description, is_initial_payment}` |
| PUT | `projects/0.1/milestone_requests/{id}/` | `?action=accept|reject|delete|release` |
| PUT | `projects/0.1/milestones/{id}/` | `{"action":"release","amount":…}` (employer, irreversible) |
| GET | `projects/0.1/invoices` | hourly invoices |
| GET | `payments/0.1/payout_accounts` | withdrawal destinations |
| GET | `memberships/0.1/history_logs/` | plan history |

### Feeds

| Method | Path | Notes |
| --- | --- | --- |
| GET | `newsfeed/0.1/newsfeed/notifications` | notifications, Elasticsearch-shaped payload |
| GET | `newsfeed/0.1/newsfeed` | activity feed |
| GET | `users/0.1/search/saved_filters?type=project` | saved searches |
| GET | `ajax-api/notifications/preferences.php` | notification settings |

## Realtime

The web app opens a SockJS connection to `https://notifications.freelancer.com`
(`/info` advertises websocket support) for live message and bid events. This
client polls instead; the socket handshake is not implemented yet.

## Wider inventory

`_raw-endpoints.txt` (528 paths with observed methods) and
`_raw-endpoint-payloads.txt` (1005 endpoint/payload combinations) are extracted
from the SPA's datastore chunks. They cover much more than this client wraps —
contests, groups, tasks, hourly contracts, invoices, equipment, enterprise. Use
them with `freelancer api` or `freelancer_api_call` when you need something that
has no dedicated command.
