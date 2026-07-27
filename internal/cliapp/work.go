package cliapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
)

func runProjects(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "projects")
	query := fs.String("query", "", "search terms")
	jobs := fs.String("jobs", "", "skill id filter, e.g. 3,305")
	types := fs.String("types", "", "project types: fixed,hourly")
	languages := fs.String("languages", "", "language codes, e.g. en,id")
	minBudget := fs.Float64("min-budget", 0, "minimum average price")
	maxBudget := fs.Float64("max-budget", 0, "maximum average price")
	sortField := fs.String("sort", "submitdate", "submitdate, bid_enddate, or bid_count")
	limit := fs.Int("limit", 20, "page size")
	offset := fs.Int("offset", 0, "result offset")
	full := fs.Bool("full", false, "include full descriptions")
	local := fs.Bool("local", false, "only local projects")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	jobIDs, err := intList(*jobs)
	if err != nil {
		return err
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	list, err := client.SearchProjects(ctx, freelancer.ProjectSearch{
		Query:           *query,
		Jobs:            jobIDs,
		ProjectTypes:    stringList(*types),
		Languages:       stringList(*languages),
		MinBudget:       *minBudget,
		MaxBudget:       *maxBudget,
		SortField:       *sortField,
		Limit:           *limit,
		Offset:          *offset,
		FullDescription: *full,
		OnlyLocal:       *local,
	})
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, list)
	}
	base := client.Config().WebBase
	for _, project := range list.Projects {
		budget := "-"
		if project.Budget != nil {
			sign := ""
			if project.Currency != nil {
				sign = project.Currency.Sign
			}
			budget = fmt.Sprintf("%s%.0f-%.0f", sign, project.Budget.Minimum, project.Budget.Maximum)
		}
		bids := 0
		if project.BidStats != nil {
			bids = project.BidStats.BidCount
		}
		fmt.Fprintf(e.stdout, "%-10d %-52s %-14s %-6s %3d bids  %s\n",
			project.ID, truncate(project.Title, 50), budget, project.Type, bids,
			time.Unix(project.SubmitDate, 0).Format("2006-01-02 15:04"))
		fmt.Fprintf(e.stdout, "           %s\n", project.URL(base))
	}
	fmt.Fprintf(e.stdout, "\n%d projects\n", len(list.Projects))
	return nil
}

func runProject(ctx context.Context, e *env, args []string) error {
	sub := "get"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := newFlagSet(e, "project "+sub)
	id := fs.Int64("id", 0, "project id")
	limit := fs.Int("limit", 20, "page size")
	offset := fs.Int("offset", 0, "result offset")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	switch sub {
	case "get":
		if *id == 0 {
			fmt.Fprintln(e.stderr, "usage: freelancer project get --id 40608147")
			return ErrUsage
		}
		raw, err := client.Project(ctx, *id)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "bids":
		if *id == 0 {
			fmt.Fprintln(e.stderr, "usage: freelancer project bids --id 40608147")
			return ErrUsage
		}
		raw, err := client.ProjectBids(ctx, *id, *limit)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "mine":
		raw, err := client.MyProjects(ctx, *limit, *offset)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	default:
		fmt.Fprintf(e.stderr, "unknown project subcommand %q (get, bids, mine)\n", sub)
		return ErrUsage
	}
}

func runBids(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "bids")
	statuses := fs.String("status", "", "frontend statuses: active,in_progress,complete,awarded,pending,rejected,withdrawn")
	projects := fs.String("projects", "", "filter by project ids")
	limit := fs.Int("limit", 20, "page size")
	offset := fs.Int("offset", 0, "result offset")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	projectIDs, err := intList(*projects)
	if err != nil {
		return err
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	list, err := client.Bids(ctx, freelancer.BidListOptions{
		Projects:         projectIDs,
		FrontendStatuses: stringList(*statuses),
		Limit:            *limit,
		Offset:           *offset,
	})
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, list)
	}
	for _, bid := range list.Bids {
		fmt.Fprintf(e.stdout, "%-11d project %-10d %8.2f  %2dd  award=%-9s paid=%-9s %s\n",
			bid.ID, bid.ProjectID, bid.Amount, bid.Period,
			orDash(bid.AwardStatus), orDash(bid.PaidStatus),
			time.Unix(bid.SubmitDate, 0).Format("2006-01-02"))
	}
	fmt.Fprintf(e.stdout, "\n%d bids\n", len(list.Bids))
	return nil
}

func runBid(ctx context.Context, e *env, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "place":
		return bidPlace(ctx, e, args)
	case "update":
		return bidUpdate(ctx, e, args)
	case "action":
		return bidAction(ctx, e, args)
	case "retract":
		return bidShortcut(ctx, e, args, freelancer.BidActionRetract)
	case "highlight":
		return bidShortcut(ctx, e, args, freelancer.BidActionHighlight)
	case "award":
		return bidShortcut(ctx, e, args, freelancer.BidActionAward)
	case "accept":
		return bidShortcut(ctx, e, args, freelancer.BidActionAccept)
	default:
		fmt.Fprintln(e.stderr, "usage: freelancer bid <place|update|action|retract|highlight|award|accept> [flags]")
		fmt.Fprintf(e.stderr, "actions: %s\n", strings.Join(freelancer.BidActions(), ", "))
		return ErrUsage
	}
}

func bidPlace(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "bid place")
	project := fs.Int64("project", 0, "project id")
	amount := fs.Float64("amount", 0, "bid amount in the project currency")
	period := fs.Int("days", 0, "delivery time in days")
	milestone := fs.Int("milestone", 50, "upfront milestone percentage")
	description := fs.String("proposal", "", "proposal text")
	proposalFile := fs.String("proposal-file", "", "read the proposal from a file")
	profileID := fs.Int64("profile-id", 0, "specialised profile id to bid from")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *proposalFile != "" {
		data, err := os.ReadFile(*proposalFile)
		if err != nil {
			return fmt.Errorf("read proposal: %w", err)
		}
		*description = strings.TrimSpace(string(data))
	}
	if *project == 0 || *amount <= 0 || *period <= 0 || *description == "" {
		fmt.Fprintln(e.stderr, "usage: freelancer bid place --project ID --amount 250 --days 7 --proposal TEXT")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.PlaceBid(ctx, freelancer.BidInput{
		ProjectID:           *project,
		Amount:              *amount,
		Period:              *period,
		MilestonePercentage: *milestone,
		Description:         *description,
		ProfileID:           *profileID,
	})
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func bidUpdate(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "bid update")
	id := fs.Int64("id", 0, "bid id")
	amount := fs.Float64("amount", 0, "new amount")
	period := fs.Int("days", 0, "new delivery time in days")
	milestone := fs.Int("milestone", 0, "new milestone percentage")
	description := fs.String("proposal", "", "new proposal text")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *id == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer bid update --id BID_ID [--amount N] [--days N] [--proposal TEXT]")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.UpdateBid(ctx, *id, freelancer.BidInput{
		Amount:              *amount,
		Period:              *period,
		MilestonePercentage: *milestone,
		Description:         *description,
	})
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func bidAction(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "bid action")
	id := fs.Int64("id", 0, "bid id")
	action := fs.String("action", "", strings.Join(freelancer.BidActions(), ", "))
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *id == 0 || *action == "" {
		fmt.Fprintf(e.stderr, "usage: freelancer bid action --id BID_ID --action <%s>\n", strings.Join(freelancer.BidActions(), "|"))
		return ErrUsage
	}
	return applyBidAction(ctx, e, *id, *action)
}

func bidShortcut(ctx context.Context, e *env, args []string, action string) error {
	fs := newFlagSet(e, "bid "+action)
	id := fs.Int64("id", 0, "bid id")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *id == 0 {
		fmt.Fprintf(e.stderr, "usage: freelancer bid %s --id BID_ID\n", action)
		return ErrUsage
	}
	return applyBidAction(ctx, e, *id, action)
}

func applyBidAction(ctx context.Context, e *env, bidID int64, action string) error {
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.BidAction(ctx, bidID, action, nil)
	if err != nil {
		return err
	}
	if raw == nil {
		fmt.Fprintf(e.stdout, "bid %d: %s applied\n", bidID, action)
		return nil
	}
	return writeRaw(e, raw)
}

func runQuota(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "quota")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	quota, err := client.BidQuota(ctx)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, quota)
	}
	fmt.Fprintf(e.stdout, "bids remaining %d of %d\n", quota.BidsRemaining, quota.BidLimit)
	fmt.Fprintf(e.stdout, "unlimited      %t\n", quota.UnlimitedBids)
	fmt.Fprintf(e.stdout, "refresh in     %s\n", (time.Duration(quota.BidRefreshTime) * time.Second).String())
	return nil
}

func runLimits(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "limits")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	limits, err := client.AccountLimits(ctx)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, limits)
	}
	fmt.Fprintf(e.stdout, "account      %s (%d)\n", limits.Username, limits.UserID)
	if limits.UnlimitedBids {
		fmt.Fprintln(e.stdout, "bids         unlimited")
	} else {
		fmt.Fprintf(e.stdout, "bids         %d of %d left", limits.BidsRemaining, limits.BidLimit)
		if limits.BidsRefillIn != "" {
			fmt.Fprintf(e.stdout, ", refills in %s", limits.BidsRefillIn)
		}
		fmt.Fprintln(e.stdout)
	}
	if limits.MaxBidUSD > 0 {
		fmt.Fprintf(e.stdout, "bid ceiling  under $%.0f USD per project\n", limits.MaxBidUSD)
	} else {
		fmt.Fprintln(e.stdout, "bid ceiling  none")
	}
	fmt.Fprintf(e.stdout, "featured     %s\n", boolLabel(limits.CanBidFeatured, "biddable", "blocked"))
	fmt.Fprintf(e.stdout, "verified     email=%t phone=%t payment=%t identity=%t\n",
		limits.EmailVerified, limits.PhoneVerified, limits.PaymentVerified, limits.IdentityVerified)
	fmt.Fprintf(e.stdout, "reputation   %d reviews, rating %.1f\n", limits.ReviewCount, limits.Rating)
	fmt.Fprintf(e.stdout, "membership   %s\n", boolLabel(limits.PaidMembership, "paid", "free"))
	fmt.Fprintf(e.stdout, "profile      %d skills, %d portfolio items, $%.2f/hr\n",
		limits.SkillCount, limits.PortfolioItems, limits.HourlyRate)
	for _, blocker := range limits.Blockers {
		fmt.Fprintf(e.stdout, "BLOCKED      %s\n", blocker)
	}
	for _, note := range limits.Notes {
		fmt.Fprintf(e.stdout, "note         %s\n", note)
	}
	return nil
}

func runMessages(ctx context.Context, e *env, args []string) error {
	sub := "threads"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "threads":
		return messagesThreads(ctx, e, args)
	case "show":
		return messagesShow(ctx, e, args)
	case "send":
		return messagesSend(ctx, e, args)
	case "read":
		return messagesAction(ctx, e, args, freelancer.ThreadActionRead)
	case "action":
		return messagesAction(ctx, e, args, "")
	case "new":
		return messagesNew(ctx, e, args)
	case "attachments":
		return messagesAttachments(ctx, e, args)
	case "search":
		return messagesSearch(ctx, e, args)
	default:
		fmt.Fprintf(e.stderr, "unknown messages subcommand %q\n", sub)
		fmt.Fprintln(e.stderr, "available: threads, show, send, read, action, new, attachments, search")
		return ErrUsage
	}
}

func messagesThreads(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages threads")
	folders := fs.String("folders", "", "inbox,sent,archived,requests")
	unread := fs.Bool("unread", false, "only unread threads")
	limit := fs.Int("limit", 20, "page size")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.Threads(ctx, freelancer.ThreadListOptions{
		Folders:    stringList(*folders),
		UnreadOnly: *unread,
		Limit:      *limit,
	})
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func messagesShow(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages show")
	thread := fs.Int64("thread", 0, "thread id")
	limit := fs.Int("limit", 30, "message count")
	offset := fs.Int("offset", 0, "message offset")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *thread == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer messages show --thread THREAD_ID")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.Messages(ctx, *thread, *limit, *offset)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func messagesSend(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages send")
	thread := fs.Int64("thread", 0, "thread id")
	message := fs.String("message", "", "message text")
	file := fs.String("file", "", "attachment path (repeatable via comma separated list)")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *thread == 0 || (*message == "" && *file == "") {
		fmt.Fprintln(e.stderr, "usage: freelancer messages send --thread ID --message TEXT [--file path]")
		return ErrUsage
	}
	var files []freelancer.FileUpload
	for _, path := range stringList(*file) {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read attachment: %w", err)
		}
		files = append(files, freelancer.FileUpload{Field: "files[]", Name: filepath.Base(path), Data: data})
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.SendMessage(ctx, *thread, *message, files)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

// messagesAction handles both `messages read` (fixed action) and
// `messages action --action star`, so the flag set must always accept --action:
// parsing it in a separate pass and re-parsing the same args broke every
// non-read action.
func messagesAction(ctx context.Context, e *env, args []string, fixed string) error {
	name := "action"
	if fixed != "" {
		name = fixed
	}
	fs := newFlagSet(e, "messages "+name)
	threads := fs.String("threads", "", "thread ids")
	thread := fs.Int64("thread", 0, "single thread id")
	action := fs.String("action", fixed, strings.Join(freelancer.ThreadActions(), ", "))
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if fixed != "" {
		*action = fixed
	}
	ids, err := intList(*threads)
	if err != nil {
		return err
	}
	if *thread != 0 {
		ids = append(ids, *thread)
	}
	if len(ids) == 0 || *action == "" {
		fmt.Fprintf(e.stderr, "usage: freelancer messages action --action <%s> --thread THREAD_ID\n",
			strings.Join(freelancer.ThreadActions(), "|"))
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.ThreadAction(ctx, ids, *action)
	if err != nil {
		return err
	}
	if raw == nil {
		fmt.Fprintf(e.stdout, "%s applied to %d thread(s)\n", *action, len(ids))
		return nil
	}
	return writeRaw(e, raw)
}

func messagesNew(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages new")
	members := fs.String("members", "", "other participant user ids")
	contextType := fs.String("context-type", "none", "project, contest, group, or none")
	contextID := fs.Int64("context", 0, "context id, e.g. the project id")
	message := fs.String("message", "", "first message")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	ids, err := intList(*members)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer messages new --members USER_ID --message TEXT [--context-type project --context PROJECT_ID]")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.CreateThread(ctx, freelancer.NewThread{
		Members:     ids,
		ContextType: *contextType,
		ContextID:   *contextID,
		Message:     *message,
	})
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func messagesAttachments(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages attachments")
	thread := fs.Int64("thread", 0, "thread id")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *thread == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer messages attachments --thread THREAD_ID")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.ThreadAttachments(ctx, *thread)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func messagesSearch(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "messages search")
	thread := fs.Int64("thread", 0, "thread id")
	query := fs.String("query", "", "search text")
	limit := fs.Int("limit", 20, "result count")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *thread == 0 || *query == "" {
		fmt.Fprintln(e.stderr, "usage: freelancer messages search --thread ID --query TEXT")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.SearchMessages(ctx, *thread, *query, *limit)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func runMilestones(ctx context.Context, e *env, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := newFlagSet(e, "milestones "+sub)
	projects := fs.String("projects", "", "project id filter")
	statuses := fs.String("statuses", "", "status filter for requests")
	limit := fs.Int("limit", 20, "page size")
	project := fs.Int64("project", 0, "project id")
	bid := fs.Int64("bid", 0, "bid id")
	amount := fs.Float64("amount", 0, "amount to request or release")
	description := fs.String("description", "", "request description")
	initial := fs.Bool("initial", false, "mark as the initial payment")
	id := fs.Int64("id", 0, "milestone or request id")
	action := fs.String("action", "", "accept, reject, delete, release")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	projectIDs, err := intList(*projects)
	if err != nil {
		return err
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		raw, err := client.Milestones(ctx, projectIDs, *limit)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "requests":
		raw, err := client.MilestoneRequests(ctx, projectIDs, stringList(*statuses), *limit)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "request":
		raw, err := client.RequestMilestone(ctx, freelancer.MilestoneRequestInput{
			ProjectID:      *project,
			BidID:          *bid,
			Amount:         *amount,
			Description:    *description,
			InitialPayment: *initial,
		})
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "action":
		if *id == 0 || *action == "" {
			fmt.Fprintln(e.stderr, "usage: freelancer milestones action --id REQUEST_ID --action accept|reject|delete|release")
			return ErrUsage
		}
		raw, err := client.MilestoneRequestAction(ctx, *id, *action)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "release":
		if *id == 0 {
			fmt.Fprintln(e.stderr, "usage: freelancer milestones release --id MILESTONE_ID [--amount N]")
			return ErrUsage
		}
		raw, err := client.ReleaseMilestone(ctx, *id, *amount)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	default:
		fmt.Fprintf(e.stderr, "unknown milestones subcommand %q (list, requests, request, action, release)\n", sub)
		return ErrUsage
	}
}

func runMoney(ctx context.Context, e *env, args []string) error {
	sub := "balances"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := newFlagSet(e, "money "+sub)
	limit := fs.Int("limit", 20, "page size")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	switch sub {
	case "balances":
		balances, err := client.Balances(ctx)
		if err != nil {
			return err
		}
		if e.jsonOut {
			return writeJSON(e, balances)
		}
		for _, balance := range balances {
			code := ""
			if balance.Currency != nil {
				code = balance.Currency.Code
			}
			fmt.Fprintf(e.stdout, "%10.2f %s\n", balance.Amount, code)
		}
		return nil
	case "invoices":
		raw, err := client.Invoices(ctx, *limit)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "payout-accounts":
		raw, err := client.PayoutAccounts(ctx)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case "membership":
		raw, err := client.Memberships(ctx)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	default:
		fmt.Fprintf(e.stderr, "unknown money subcommand %q (balances, invoices, payout-accounts, membership)\n", sub)
		return ErrUsage
	}
}

func runNotifications(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "notifications")
	limit := fs.Int("limit", 20, "item count")
	feed := fs.Bool("feed", false, "activity feed instead of notifications")
	saved := fs.Bool("saved-searches", false, "list saved project searches")
	prefs := fs.Bool("preferences", false, "notification preferences")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	switch {
	case *saved:
		raw, err = client.SavedSearches(ctx)
	case *prefs:
		raw, err = client.NotificationPreferences(ctx)
	case *feed:
		raw, err = client.Newsfeed(ctx, *limit)
	default:
		raw, err = client.Notifications(ctx, *limit)
	}
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func runReviews(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "reviews")
	user := fs.Int64("user", 0, "user id (default: yourself)")
	role := fs.String("role", "freelancer", "freelancer or employer")
	limit := fs.Int("limit", 20, "page size")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.Reviews(ctx, *user, *role, *limit)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func runSkills(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "skills")
	query := fs.String("query", "", "filter by name substring")
	limit := fs.Int("limit", 40, "maximum rows")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	jobs, err := client.Jobs(ctx)
	if err != nil {
		return err
	}
	needle := strings.ToLower(*query)
	matched := make([]freelancer.Job, 0, len(jobs))
	for _, job := range jobs {
		if needle == "" || strings.Contains(strings.ToLower(job.Name), needle) {
			matched = append(matched, job)
		}
	}
	if *limit > 0 && len(matched) > *limit {
		matched = matched[:*limit]
	}
	if e.jsonOut {
		return writeJSON(e, matched)
	}
	for _, job := range matched {
		category := ""
		if job.Category != nil {
			category = job.Category.Name
		}
		fmt.Fprintf(e.stdout, "%-8d %-32s %s\n", job.ID, job.Name, category)
	}
	fmt.Fprintf(e.stdout, "\n%d skills\n", len(matched))
	return nil
}

func runCurrencies(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "currencies")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	currencies, err := client.Currencies(ctx)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, currencies)
	}
	for _, currency := range currencies {
		fmt.Fprintf(e.stdout, "%-4d %-5s %-24s %s\n", currency.ID, currency.Code, currency.Name, currency.Sign)
	}
	return nil
}

func runFreelancers(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "freelancers")
	query := fs.String("query", "", "search terms")
	jobs := fs.String("jobs", "", "skill ids")
	countries := fs.String("countries", "", "country codes")
	minRate := fs.Float64("min-rate", 0, "minimum hourly rate")
	maxRate := fs.Float64("max-rate", 0, "maximum hourly rate")
	online := fs.Bool("online", false, "only online freelancers")
	limit := fs.Int("limit", 10, "page size")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	jobIDs, err := intList(*jobs)
	if err != nil {
		return err
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.SearchFreelancers(ctx, freelancer.FreelancerSearch{
		Query:         *query,
		Jobs:          jobIDs,
		Countries:     stringList(*countries),
		HourlyRateMin: *minRate,
		HourlyRateMax: *maxRate,
		OnlineOnly:    *online,
		Limit:         *limit,
	})
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func runAPI(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "api")
	method := fs.String("method", "GET", "HTTP method")
	base := fs.String("base", "api", "api or web")
	data := fs.String("data", "", "JSON body")
	form := fs.String("form", "", "urlencoded body, e.g. jobs[]=3&jobs[]=9")
	query := fs.String("query", "", "query string, e.g. limit=5&users[]=1")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer api [--method PUT] [--base api|web] [--query k=v] [--data JSON] /users/0.1/self/")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	req := freelancer.Request{Method: *method, Path: rest[0]}
	switch *base {
	case "web":
		req.Base = client.Config().WebBase
	default:
		req.Base = client.Config().APIBase
	}
	if *query != "" {
		values, err := url.ParseQuery(*query)
		if err != nil {
			return fmt.Errorf("parse query: %w", err)
		}
		req.Query = values
	}
	if *form != "" {
		values, err := url.ParseQuery(*form)
		if err != nil {
			return fmt.Errorf("parse form: %w", err)
		}
		req.Form = values
	}
	if *data != "" {
		var payload any
		if err := json.Unmarshal([]byte(*data), &payload); err != nil {
			return fmt.Errorf("parse --data as JSON: %w", err)
		}
		req.JSON = payload
	}
	resp, err := client.Do(ctx, req)
	if err != nil {
		return err
	}
	return writeRaw(e, resp.Body)
}

func decodeJSONObject(value string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return nil, fmt.Errorf("parse JSON payload: %w", err)
	}
	return payload, nil
}
