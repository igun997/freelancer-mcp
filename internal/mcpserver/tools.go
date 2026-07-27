package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
)

type toolHandler func(ctx context.Context, s *Server, args map[string]any) (any, error)

type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Handler     toolHandler
	// SkipSession is set for tools that must work without a valid session.
	SkipSession bool
}

func obj(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any   { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any   { return map[string]any{"type": "integer", "description": desc} }
func float(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
func flag(desc string) map[string]any  { return map[string]any{"type": "boolean", "description": desc} }
func ints(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": desc}
}
func strs(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func tools() []tool {
	return append(append(accountTools(), workTools()...), moneyTools()...)
}

func accountTools() []tool {
	return []tool{
		{
			Name:        "freelancer_whoami",
			Description: "Show the authenticated freelancer.com account and session state.",
			Schema:      obj(map[string]any{}),
			SkipSession: true,
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				sess := s.client.Session()
				payload := map[string]any{
					"profile":      sess.Profile,
					"session_file": s.store.Path(),
					"user_id":      sess.UserID,
					"username":     sess.Username,
					"email":        sess.Email,
					"role":         sess.Role,
					"has_token":    sess.Token != "",
				}
				self, err := s.client.EnsureSession(ctx)
				if err != nil {
					payload["error"] = err.Error()
					return payload, nil
				}
				payload["user"] = self
				return payload, nil
			},
		},
		{
			Name:        "freelancer_profile_get",
			Description: "Read a profile: tagline, summary, hourly rate, skills, location. Omit user_id for your own profile.",
			Schema:      obj(map[string]any{"user_id": num("user id, omit for yourself")}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				return s.client.Profile(ctx, argInt64(args, "user_id", 0))
			},
		},
		{
			Name: "freelancer_profile_update",
			Description: "Update your profile. tagline is the professional headline, summary is the profile description " +
				"(Freelancer enforces a 100 character minimum), hourly_rate is in the account currency. Only the fields you send change.",
			Schema: obj(map[string]any{
				"tagline":     str("professional headline"),
				"summary":     str("profile summary, at least 100 characters"),
				"hourly_rate": float("hourly rate in the account currency"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				update := freelancer.ProfileUpdate{}
				if value, ok := args["tagline"].(string); ok {
					update.Tagline = &value
				}
				if value, ok := args["summary"].(string); ok {
					update.ProfileDescription = &value
				}
				if value, ok := args["hourly_rate"].(float64); ok {
					update.HourlyRate = &value
				}
				if err := s.client.UpdateProfile(ctx, update); err != nil {
					return nil, err
				}
				return s.client.Profile(ctx, 0)
			},
		},
		{
			Name: "freelancer_profile_skills",
			Description: "Manage profile skills. action: list (default), set (replace), add, remove. " +
				"job_ids come from freelancer_skills_search.",
			Schema: obj(map[string]any{
				"action":  str("list, set, add, or remove"),
				"job_ids": ints("skill (job) ids"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				action := argString(args, "action")
				ids := argInt64Slice(args, "job_ids")
				var err error
				switch action {
				case "", "list":
				case "set":
					err = s.client.SetSkills(ctx, ids)
				case "add":
					err = s.client.AddSkills(ctx, ids)
				case "remove":
					err = s.client.RemoveSkills(ctx, ids)
				default:
					return nil, fmt.Errorf("unknown action %q: use list, set, add, or remove", action)
				}
				if err != nil {
					return nil, err
				}
				profile, err := s.client.Profile(ctx, 0)
				if err != nil {
					return nil, err
				}
				return profile.Jobs, nil
			},
		},
		{
			Name:        "freelancer_skills_search",
			Description: "Search the skill (job) catalogue by name and return matching ids for bidding and profile updates.",
			Schema: obj(map[string]any{
				"query": str("name substring, e.g. golang"),
				"limit": num("maximum rows, default 25"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				jobs, err := s.client.Jobs(ctx)
				if err != nil {
					return nil, err
				}
				needle := strings.ToLower(argString(args, "query"))
				limit := argInt(args, "limit", 25)
				matched := make([]freelancer.Job, 0, limit)
				for _, job := range jobs {
					if needle != "" && !strings.Contains(strings.ToLower(job.Name), needle) {
						continue
					}
					matched = append(matched, job)
					if len(matched) >= limit {
						break
					}
				}
				return matched, nil
			},
		},
		{
			Name: "freelancer_profile_cv",
			Description: "Manage CV records. section: experience, education, publication, certification. " +
				"action: add (default), update, delete. Required fields per section are reported when the payload is incomplete.",
			Schema: obj(map[string]any{
				"section": str("experience, education, publication, or certification"),
				"action":  str("add, update, or delete"),
				"id":      num("entry id for update or delete"),
				"payload": map[string]any{"type": "object", "description": "entry fields, e.g. {title, company, start_date}"},
			}, "section"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				kind, err := freelancer.ParseCVEntryKind(argString(args, "section"))
				if err != nil {
					return nil, err
				}
				action := argString(args, "action")
				if action == "" {
					action = "add"
				}
				id := argInt64(args, "id", 0)
				payload, _ := args["payload"].(map[string]any)
				switch action {
				case "add":
					if len(payload) == 0 {
						return nil, fmt.Errorf("%s needs %v", kind, kind.RequiredFields())
					}
					data, err := s.client.AddCVEntry(ctx, kind, payload)
					return rawOrNull(data), err
				case "update":
					if id == 0 {
						return nil, fmt.Errorf("id is required to update a %s entry", kind)
					}
					data, err := s.client.UpdateCVEntry(ctx, kind, id, payload)
					return rawOrNull(data), err
				case "delete":
					if id == 0 {
						return nil, fmt.Errorf("id is required to delete a %s entry", kind)
					}
					if err := s.client.DeleteCVEntry(ctx, kind, id); err != nil {
						return nil, err
					}
					return map[string]any{"section": string(kind), "id": id, "deleted": true}, nil
				default:
					return nil, fmt.Errorf("unknown action %q: use add, update, or delete", action)
				}
			},
		},
		{
			Name:        "freelancer_portfolio_list",
			Description: "List portfolio items. Omit user_id for your own portfolio.",
			Schema:      obj(map[string]any{"user_id": num("user id, omit for yourself")}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				var ids []int64
				if id := argInt64(args, "user_id", 0); id != 0 {
					ids = []int64{id}
				}
				data, err := s.client.Portfolios(ctx, ids)
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_profile_picture_upload",
			Description: "Replace the profile picture with a local image file. Crop values are pixels on the uploaded image.",
			Schema: obj(map[string]any{
				"path":   str("local image path"),
				"x":      num("crop offset x, default 0"),
				"y":      num("crop offset y, default 0"),
				"width":  num("crop width in pixels"),
				"height": num("crop height in pixels"),
			}, "path", "width", "height"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				path := argString(args, "path")
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, fmt.Errorf("read image: %w", err)
				}
				raw, err := s.client.UploadProfilePicture(ctx, filepath.Base(path), data,
					argInt(args, "x", 0), argInt(args, "y", 0),
					argInt(args, "width", 0), argInt(args, "height", 0))
				return rawOrNull(raw), err
			},
		},
		{
			Name:        "freelancer_reputation",
			Description: "Rating, earnings history, and project stats for a user in one role.",
			Schema: obj(map[string]any{
				"user_id": num("user id, omit for yourself"),
				"role":    str("freelancer (default) or employer"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				id := argInt64(args, "user_id", 0)
				if id == 0 {
					id = s.client.UserID()
				}
				data, err := s.client.Reputation(ctx, []int64{id}, argString(args, "role"))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_reviews",
			Description: "Reviews written about a user. Omit user_id for your own reviews.",
			Schema: obj(map[string]any{
				"user_id": num("user id, omit for yourself"),
				"role":    str("freelancer (default) or employer"),
				"limit":   num("page size, default 20"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Reviews(ctx, argInt64(args, "user_id", 0), argString(args, "role"), argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_account_settings",
			Description: "Change account settings: chosen_role switches between freelancer and employer, currency_id sets the account currency.",
			Schema: obj(map[string]any{
				"chosen_role": str("freelancer or employer"),
				"currency_id": num("currency id from freelancer_currencies"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				changed := map[string]any{}
				if role := argString(args, "chosen_role"); role != "" {
					if err := s.client.SetChosenRole(ctx, role); err != nil {
						return nil, err
					}
					changed["chosen_role"] = role
				}
				if id := argInt64(args, "currency_id", 0); id != 0 {
					if err := s.client.SetPrimaryCurrency(ctx, id); err != nil {
						return nil, err
					}
					changed["currency_id"] = id
				}
				if len(changed) == 0 {
					return nil, fmt.Errorf("nothing to change: set chosen_role or currency_id")
				}
				return changed, nil
			},
		},
		{
			Name:        "freelancer_currencies",
			Description: "List supported currencies with their ids.",
			Schema:      obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				return s.client.Currencies(ctx)
			},
		},
		{
			Name:        "freelancer_freelancers_search",
			Description: "Search the public freelancer directory by skills, rate, country, or keywords.",
			Schema: obj(map[string]any{
				"query":       str("search terms"),
				"job_ids":     ints("skill ids"),
				"countries":   strs("country codes, e.g. ID, US"),
				"min_rate":    float("minimum hourly rate"),
				"max_rate":    float("maximum hourly rate"),
				"online_only": flag("only freelancers currently online"),
				"limit":       num("page size, default 10"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.SearchFreelancers(ctx, freelancer.FreelancerSearch{
					Query:         argString(args, "query"),
					Jobs:          argInt64Slice(args, "job_ids"),
					Countries:     argStringSlice(args, "countries"),
					HourlyRateMin: argFloat(args, "min_rate", 0),
					HourlyRateMax: argFloat(args, "max_rate", 0),
					OnlineOnly:    argBool(args, "online_only"),
					Limit:         argInt(args, "limit", 10),
				})
				return rawOrNull(data), err
			},
		},
	}
}

func workTools() []tool {
	return []tool{
		{
			Name: "freelancer_projects_search",
			Description: "Search the active project feed. Combine query with job_ids (from freelancer_skills_search), " +
				"budget bounds, and project_types (fixed, hourly).",
			Schema: obj(map[string]any{
				"query":            str("search terms"),
				"job_ids":          ints("skill ids to filter on"),
				"project_types":    strs("fixed, hourly"),
				"languages":        strs("language codes, e.g. en"),
				"min_budget":       float("minimum average price"),
				"max_budget":       float("maximum average price"),
				"sort_field":       str("submitdate (default), bid_enddate, bid_count"),
				"limit":            num("page size, default 20"),
				"offset":           num("result offset"),
				"full_description": flag("include the whole brief instead of a preview"),
				"only_local":       flag("only local projects"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				return s.client.SearchProjects(ctx, freelancer.ProjectSearch{
					Query:           argString(args, "query"),
					Jobs:            argInt64Slice(args, "job_ids"),
					ProjectTypes:    argStringSlice(args, "project_types"),
					Languages:       argStringSlice(args, "languages"),
					MinBudget:       argFloat(args, "min_budget", 0),
					MaxBudget:       argFloat(args, "max_budget", 0),
					SortField:       argString(args, "sort_field"),
					Limit:           argInt(args, "limit", 20),
					Offset:          argInt(args, "offset", 0),
					FullDescription: argBool(args, "full_description"),
					OnlyLocal:       argBool(args, "only_local"),
				})
			},
		},
		{
			Name:        "freelancer_project_get",
			Description: "Full project brief with budget, attachments, owner reputation, and selected bids.",
			Schema:      obj(map[string]any{"project_id": num("project id")}, "project_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Project(ctx, argInt64(args, "project_id", 0))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_project_bids",
			Description: "Competing bids on a project, with bidder reputation.",
			Schema: obj(map[string]any{
				"project_id": num("project id"),
				"limit":      num("page size, default 20"),
			}, "project_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.ProjectBids(ctx, argInt64(args, "project_id", 0), argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_projects_mine",
			Description: "Projects you posted as an employer, with selected bids.",
			Schema: obj(map[string]any{
				"limit":  num("page size, default 20"),
				"offset": num("result offset"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.MyProjects(ctx, argInt(args, "limit", 20), argInt(args, "offset", 0))
				return rawOrNull(data), err
			},
		},
		{
			Name: "freelancer_project_post",
			Description: "Publish a project as an employer. This is publicly visible and spends posting quota, " +
				"so confirm must be true and the user must have asked for it.",
			Schema: obj(map[string]any{
				"title":       str("project title"),
				"description": str("project brief"),
				"job_ids":     ints("skill ids"),
				"currency_id": num("currency id, default 1 (USD)"),
				"budget_min":  float("minimum budget"),
				"budget_max":  float("maximum budget"),
				"hourly":      flag("post as an hourly project"),
				"hours":       num("hours per interval for hourly projects"),
				"interval":    str("week (default) or month"),
				"confirm":     flag("must be true to publish"),
			}, "title", "description", "job_ids", "confirm"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				if !argBool(args, "confirm") {
					return nil, fmt.Errorf("refusing to publish a project without confirm=true")
				}
				data, err := s.client.PostProject(ctx, freelancer.PostProject{
					Title:          argString(args, "title"),
					Description:    argString(args, "description"),
					JobIDs:         argInt64Slice(args, "job_ids"),
					CurrencyID:     argInt64(args, "currency_id", 1),
					BudgetMin:      argFloat(args, "budget_min", 0),
					BudgetMax:      argFloat(args, "budget_max", 0),
					Hourly:         argBool(args, "hourly"),
					HourlyHours:    argInt(args, "hours", 0),
					HourlyInterval: argString(args, "interval"),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name: "freelancer_bids_list",
			Description: "List your bids. status accepts the web app buckets: active, in_progress, complete, " +
				"awarded, pending, rejected, withdrawn.",
			Schema: obj(map[string]any{
				"status":      strs("frontend bid statuses"),
				"project_ids": ints("filter by project ids"),
				"limit":       num("page size, default 20"),
				"offset":      num("result offset"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				return s.client.Bids(ctx, freelancer.BidListOptions{
					Projects:         argInt64Slice(args, "project_ids"),
					FrontendStatuses: argStringSlice(args, "status"),
					Limit:            argInt(args, "limit", 20),
					Offset:           argInt(args, "offset", 0),
				})
			},
		},
		{
			Name: "freelancer_bid_quota",
			Description: "Remaining monthly bid allowance and when it refills. Check this before bidding, " +
				"a rejected bid still burns the user's attention.",
			Schema: obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				return s.client.BidQuota(ctx)
			},
		},
		{
			Name: "freelancer_bid_place",
			Description: "Submit a proposal on a project. Spends one bid from the monthly quota and is immediately " +
				"visible to the client, so confirm must be true and the proposal text must be the user's own offer.",
			Schema: obj(map[string]any{
				"project_id":           num("project id"),
				"amount":               float("bid amount in the project currency"),
				"period":               num("delivery time in days"),
				"description":          str("proposal text"),
				"milestone_percentage": num("upfront milestone share, default 50"),
				"profile_id":           num("specialised profile id to bid from"),
				"confirm":              flag("must be true to submit"),
			}, "project_id", "amount", "period", "description", "confirm"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				if !argBool(args, "confirm") {
					return nil, fmt.Errorf("refusing to place a bid without confirm=true")
				}
				data, err := s.client.PlaceBid(ctx, freelancer.BidInput{
					ProjectID:           argInt64(args, "project_id", 0),
					Amount:              argFloat(args, "amount", 0),
					Period:              argInt(args, "period", 0),
					Description:         argString(args, "description"),
					MilestonePercentage: argInt(args, "milestone_percentage", 0),
					ProfileID:           argInt64(args, "profile_id", 0),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_bid_update",
			Description: "Edit a live proposal: amount, delivery days, milestone percentage, or text.",
			Schema: obj(map[string]any{
				"bid_id":               num("bid id"),
				"amount":               float("new amount"),
				"period":               num("new delivery time in days"),
				"milestone_percentage": num("new milestone percentage"),
				"description":          str("new proposal text"),
			}, "bid_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.UpdateBid(ctx, argInt64(args, "bid_id", 0), freelancer.BidInput{
					Amount:              argFloat(args, "amount", 0),
					Period:              argInt(args, "period", 0),
					MilestonePercentage: argInt(args, "milestone_percentage", 0),
					Description:         argString(args, "description"),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name: "freelancer_bid_action",
			Description: "Apply an action to a bid. Freelancer side: retract, highlight, sponsor, accept, deny. " +
				"Employer side: award, revoke, shortlist, unshortlist, hide, unhide. retract, award, and revoke " +
				"cannot be undone, so they need confirm=true.",
			Schema: obj(map[string]any{
				"bid_id":  num("bid id"),
				"action":  str(strings.Join(freelancer.BidActions(), ", ")),
				"confirm": flag("required for retract, award, revoke, deny"),
			}, "bid_id", "action"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				action := argString(args, "action")
				switch action {
				case freelancer.BidActionRetract, freelancer.BidActionAward, freelancer.BidActionRevoke, freelancer.BidActionDeny:
					if !argBool(args, "confirm") {
						return nil, fmt.Errorf("%s cannot be undone: pass confirm=true", action)
					}
				}
				bidID := argInt64(args, "bid_id", 0)
				data, err := s.client.BidAction(ctx, bidID, action, nil)
				if err != nil {
					return nil, err
				}
				if data == nil {
					return map[string]any{"bid_id": bidID, "action": action, "applied": true}, nil
				}
				return rawOrNull(data), nil
			},
		},
		{
			Name:        "freelancer_manage_bids",
			Description: "Dashboard view of bids and awarded work. kind: ongoing (default), past, cancelled.",
			Schema: obj(map[string]any{
				"kind":  str("ongoing, past, or cancelled"),
				"limit": num("page size, default 50"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.ManageBids(ctx, argString(args, "kind"), argInt(args, "limit", 50))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_threads_list",
			Description: "List message threads with last message and unread counters. folders: inbox, sent, archived, requests.",
			Schema: obj(map[string]any{
				"folders":     strs("inbox, sent, archived, requests"),
				"unread_only": flag("only threads with unread messages"),
				"limit":       num("page size, default 20"),
				"offset":      num("result offset"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Threads(ctx, freelancer.ThreadListOptions{
					Folders:    argStringSlice(args, "folders"),
					UnreadOnly: argBool(args, "unread_only"),
					Limit:      argInt(args, "limit", 20),
					Offset:     argInt(args, "offset", 0),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_messages_list",
			Description: "Read the message history of one thread.",
			Schema: obj(map[string]any{
				"thread_id": num("thread id"),
				"limit":     num("message count, default 30"),
				"offset":    num("message offset"),
			}, "thread_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Messages(ctx, argInt64(args, "thread_id", 0), argInt(args, "limit", 30), argInt(args, "offset", 0))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_messages_search",
			Description: "Search inside one thread's history.",
			Schema: obj(map[string]any{
				"thread_id": num("thread id"),
				"query":     str("search text"),
				"limit":     num("result count, default 20"),
			}, "thread_id", "query"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.SearchMessages(ctx, argInt64(args, "thread_id", 0), argString(args, "query"), argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_message_send",
			Description: "Send a message into a thread, optionally attaching local files.",
			Schema: obj(map[string]any{
				"thread_id": num("thread id"),
				"message":   str("message text"),
				"paths":     strs("local file paths to attach"),
			}, "thread_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				var files []freelancer.FileUpload
				for _, path := range argStringSlice(args, "paths") {
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, fmt.Errorf("read attachment %s: %w", path, err)
					}
					files = append(files, freelancer.FileUpload{Field: "files[]", Name: filepath.Base(path), Data: data})
				}
				data, err := s.client.SendMessage(ctx, argInt64(args, "thread_id", 0), argString(args, "message"), files)
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_thread_action",
			Description: "Apply an action to threads: read, mute, unmute, block, unblock, star, unstar, archive.",
			Schema: obj(map[string]any{
				"thread_ids": ints("thread ids"),
				"action":     str(strings.Join(freelancer.ThreadActions(), ", ")),
			}, "thread_ids"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				ids := argInt64Slice(args, "thread_ids")
				action := argString(args, "action")
				data, err := s.client.ThreadAction(ctx, ids, action)
				if err != nil {
					return nil, err
				}
				if data == nil {
					return map[string]any{"threads": ids, "action": action, "applied": true}, nil
				}
				return rawOrNull(data), nil
			},
		},
		{
			Name: "freelancer_thread_new",
			Description: "Start a conversation. context_type project plus context_id keeps it attached to a project; " +
				"Freelancer rejects cold messages with no shared context.",
			Schema: obj(map[string]any{
				"member_ids":   ints("other participant user ids"),
				"context_type": str("project, contest, group, or none"),
				"context_id":   num("context id, e.g. the project id"),
				"message":      str("first message"),
			}, "member_ids"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.CreateThread(ctx, freelancer.NewThread{
					Members:     argInt64Slice(args, "member_ids"),
					ContextType: argString(args, "context_type"),
					ContextID:   argInt64(args, "context_id", 0),
					Message:     argString(args, "message"),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_thread_attachments",
			Description: "List files shared in a thread.",
			Schema:      obj(map[string]any{"thread_id": num("thread id")}, "thread_id"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.ThreadAttachments(ctx, argInt64(args, "thread_id", 0))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_notifications",
			Description: "Notification stream (bid awards, messages, project updates). Set feed=true for the activity feed.",
			Schema: obj(map[string]any{
				"limit": num("item count, default 20"),
				"feed":  flag("return the activity feed instead"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				limit := argInt(args, "limit", 20)
				if argBool(args, "feed") {
					data, err := s.client.Newsfeed(ctx, limit)
					return rawOrNull(data), err
				}
				data, err := s.client.Notifications(ctx, limit)
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_saved_searches",
			Description: "Saved project search filters, useful for repeating the user's own feed queries.",
			Schema:      obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				data, err := s.client.SavedSearches(ctx)
				return rawOrNull(data), err
			},
		},
	}
}

func moneyTools() []tool {
	return []tool{
		{
			Name:        "freelancer_balances",
			Description: "Account wallet, one entry per currency.",
			Schema:      obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				return s.client.Balances(ctx)
			},
		},
		{
			Name:        "freelancer_invoices",
			Description: "Hourly project invoices.",
			Schema:      obj(map[string]any{"limit": num("page size, default 20")}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Invoices(ctx, argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_payout_accounts",
			Description: "Withdrawal destinations configured on the account.",
			Schema:      obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				data, err := s.client.PayoutAccounts(ctx)
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_membership",
			Description: "Membership plan history (Plus, Professional, Premier) with prices and periods.",
			Schema:      obj(map[string]any{}),
			Handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
				data, err := s.client.Memberships(ctx)
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_milestones_list",
			Description: "Escrow milestones on your projects.",
			Schema: obj(map[string]any{
				"project_ids": ints("filter by project ids"),
				"limit":       num("page size, default 20"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.Milestones(ctx, argInt64Slice(args, "project_ids"), argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_milestone_requests_list",
			Description: "Milestone (payment) requests, optionally filtered by project or status.",
			Schema: obj(map[string]any{
				"project_ids": ints("filter by project ids"),
				"statuses":    strs("pending, accepted, rejected, retracted"),
				"limit":       num("page size, default 20"),
			}),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.MilestoneRequests(ctx, argInt64Slice(args, "project_ids"),
					argStringSlice(args, "statuses"), argInt(args, "limit", 20))
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_milestone_request_create",
			Description: "Ask the client to fund a milestone for awarded work.",
			Schema: obj(map[string]any{
				"project_id":      num("project id"),
				"bid_id":          num("your bid id on that project"),
				"amount":          float("amount to request"),
				"description":     str("what the payment covers"),
				"initial_payment": flag("mark as the initial payment"),
			}, "project_id", "bid_id", "amount"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.RequestMilestone(ctx, freelancer.MilestoneRequestInput{
					ProjectID:      argInt64(args, "project_id", 0),
					BidID:          argInt64(args, "bid_id", 0),
					Amount:         argFloat(args, "amount", 0),
					Description:    argString(args, "description"),
					InitialPayment: argBool(args, "initial_payment"),
				})
				return rawOrNull(data), err
			},
		},
		{
			Name:        "freelancer_milestone_request_action",
			Description: "Apply an action to a milestone request: accept, reject, delete, release.",
			Schema: obj(map[string]any{
				"request_id": num("milestone request id"),
				"action":     str("accept, reject, delete, or release"),
			}, "request_id", "action"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				data, err := s.client.MilestoneRequestAction(ctx, argInt64(args, "request_id", 0), argString(args, "action"))
				return rawOrNull(data), err
			},
		},
		{
			Name: "freelancer_milestone_release",
			Description: "Release escrow to the freelancer. Money moves immediately and cannot be recalled, " +
				"so confirm must be true and the user must have asked for it.",
			Schema: obj(map[string]any{
				"milestone_id": num("milestone id"),
				"amount":       float("partial amount, omit to release everything"),
				"confirm":      flag("must be true to release"),
			}, "milestone_id", "confirm"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				if !argBool(args, "confirm") {
					return nil, fmt.Errorf("releasing escrow is irreversible: pass confirm=true")
				}
				data, err := s.client.ReleaseMilestone(ctx, argInt64(args, "milestone_id", 0), argFloat(args, "amount", 0))
				return rawOrNull(data), err
			},
		},
		{
			Name: "freelancer_api_call",
			Description: "Escape hatch for any freelancer.com endpoint. base: api (default, www.freelancer.com/api) " +
				"or web (www.freelancer.com, for /ajax-api paths). Pass query as an object, body as JSON, " +
				"or form for urlencoded payloads such as jobs[]=3.",
			Schema: obj(map[string]any{
				"method": str("GET (default), POST, PUT, DELETE"),
				"base":   str("api or web"),
				"path":   str("endpoint path, e.g. /users/0.1/self/"),
				"query":  map[string]any{"type": "object", "description": "query parameters"},
				"body":   map[string]any{"type": "object", "description": "JSON body"},
				"form":   map[string]any{"type": "object", "description": "urlencoded body fields"},
			}, "path"),
			Handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
				req := freelancer.Request{
					Method: strings.ToUpper(argString(args, "method")),
					Path:   argString(args, "path"),
				}
				if argString(args, "base") == "web" {
					req.Base = s.client.Config().WebBase
				} else {
					req.Base = s.client.Config().APIBase
				}
				if query, ok := args["query"].(map[string]any); ok {
					req.Query = toValues(query)
				}
				if form, ok := args["form"].(map[string]any); ok {
					req.Form = toValues(form)
				}
				if body, ok := args["body"]; ok && body != nil {
					req.JSON = body
				}
				resp, err := s.client.Do(ctx, req)
				if err != nil {
					return nil, err
				}
				var parsed any
				if json.Unmarshal(resp.Body, &parsed) == nil {
					return parsed, nil
				}
				return map[string]any{"status_code": resp.StatusCode, "body": string(resp.Body)}, nil
			},
		},
	}
}

func toValues(in map[string]any) url.Values {
	out := url.Values{}
	for key, value := range in {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				out.Add(key, fmt.Sprint(item))
			}
		case float64:
			out.Set(key, strconv.FormatFloat(typed, 'f', -1, 64))
		case bool:
			out.Set(key, strconv.FormatBool(typed))
		case nil:
		default:
			out.Set(key, fmt.Sprint(typed))
		}
	}
	return out
}

// toolSchemas renders the MCP tools/list payload.
func (s *Server) toolSchemas() []map[string]any {
	all := tools()
	out := make([]map[string]any, 0, len(all))
	for _, t := range all {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.Schema,
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &call); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params: " + err.Error()}
		}
	}
	if call.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tool name is required"}
	}
	for _, t := range tools() {
		if t.Name != call.Name {
			continue
		}
		if !t.SkipSession {
			if err := s.ensureSession(ctx); err != nil {
				return toolError(err), nil
			}
		}
		result, err := t.Handler(ctx, s, call.Arguments)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool " + call.Name}
}

func toolResult(value any) (any, error) {
	text, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	}, nil
}

func toolError(err error) any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
	}
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if value, ok := args[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func argBool(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	switch value := args[key].(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(value)
		return err == nil && parsed
	}
	return false
}

func argInt(args map[string]any, key string, fallback int) int {
	return int(argInt64(args, key, int64(fallback)))
}

func argInt64(args map[string]any, key string, fallback int64) int64 {
	if args == nil {
		return fallback
	}
	switch value := args[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func argFloat(args map[string]any, key string, fallback float64) float64 {
	if args == nil {
		return fallback
	}
	switch value := args[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func argInt64Slice(args map[string]any, key string) []int64 {
	if args == nil {
		return nil
	}
	switch value := args[key].(type) {
	case []any:
		out := make([]int64, 0, len(value))
		for _, item := range value {
			switch typed := item.(type) {
			case float64:
				out = append(out, int64(typed))
			case string:
				if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
					out = append(out, parsed)
				}
			}
		}
		return out
	case string:
		parts := strings.Split(value, ",")
		out := make([]int64, 0, len(parts))
		for _, part := range parts {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
				out = append(out, parsed)
			}
		}
		return out
	}
	return nil
}

func argStringSlice(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	switch value := args[key].(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	case string:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
	return nil
}
