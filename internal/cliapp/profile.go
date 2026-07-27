package cliapp

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
)

func runProfile(ctx context.Context, e *env, args []string) error {
	sub := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		return profileShow(ctx, e, args)
	case "update":
		return profileUpdate(ctx, e, args)
	case "skills":
		return profileSkills(ctx, e, args)
	case "avatar":
		return profileAvatar(ctx, e, args)
	case "role":
		return profileRole(ctx, e, args)
	case "currency":
		return profileCurrency(ctx, e, args)
	case "cv":
		return profileCV(ctx, e, args)
	case "schools":
		return profileSchools(ctx, e, args)
	case "portfolio":
		return profilePortfolio(ctx, e, args)
	case "reputation":
		return profileReputation(ctx, e, args)
	default:
		fmt.Fprintf(e.stderr, "unknown profile subcommand %q\n", sub)
		fmt.Fprintln(e.stderr, "available: show, update, skills, avatar, role, currency, cv, schools, portfolio, reputation")
		return ErrUsage
	}
}

func profileShow(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile show")
	user := fs.Int64("user", 0, "user id (default: yourself)")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	profile, err := client.Profile(ctx, *user)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, profile)
	}
	fmt.Fprintf(e.stdout, "user id      %d\n", profile.ID)
	fmt.Fprintf(e.stdout, "username     %s\n", profile.Username)
	fmt.Fprintf(e.stdout, "display name %s\n", profile.DisplayName)
	fmt.Fprintf(e.stdout, "tagline      %s\n", orDash(profile.Tagline))
	fmt.Fprintf(e.stdout, "hourly rate  %.2f\n", profile.HourlyRate)
	fmt.Fprintf(e.stdout, "role         %s (chosen: %s)\n", profile.Role, profile.ChosenRole)
	names := make([]string, 0, len(profile.Jobs))
	for _, job := range profile.Jobs {
		names = append(names, fmt.Sprintf("%s(%d)", job.Name, job.ID))
	}
	fmt.Fprintf(e.stdout, "skills       %s\n", orDash(strings.Join(names, ", ")))
	fmt.Fprintf(e.stdout, "summary      %s\n", orDash(profile.ProfileDescription))
	return nil
}

func profileUpdate(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile update")
	tagline := fs.String("tagline", "", "professional headline")
	summary := fs.String("summary", "", "profile summary (minimum 100 characters)")
	summaryFile := fs.String("summary-file", "", "read the summary from a file")
	hourly := fs.Float64("hourly-rate", -1, "hourly rate in the account currency")
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, "usage: freelancer profile update [--tagline TEXT] [--summary TEXT|--summary-file PATH] [--hourly-rate N]")
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	update := freelancer.ProfileUpdate{}
	if set["tagline"] {
		update.Tagline = tagline
	}
	if *summaryFile != "" {
		data, err := os.ReadFile(*summaryFile)
		if err != nil {
			return fmt.Errorf("read summary file: %w", err)
		}
		text := strings.TrimSpace(string(data))
		update.ProfileDescription = &text
	} else if set["summary"] {
		update.ProfileDescription = summary
	}
	if set["hourly-rate"] && *hourly >= 0 {
		update.HourlyRate = hourly
	}
	if update.Empty() {
		fs.Usage()
		return ErrUsage
	}

	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	if err := client.UpdateProfile(ctx, update); err != nil {
		return err
	}
	profile, err := client.Profile(ctx, 0)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, profile)
	}
	fmt.Fprintln(e.stdout, "profile updated")
	fmt.Fprintf(e.stdout, "tagline      %s\n", orDash(profile.Tagline))
	fmt.Fprintf(e.stdout, "hourly rate  %.2f\n", profile.HourlyRate)
	fmt.Fprintf(e.stdout, "summary      %s\n", orDash(truncate(profile.ProfileDescription, 160)))
	return nil
}

func profileSkills(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile skills")
	set := fs.String("set", "", "replace skills with this id list, e.g. 3,77,305")
	add := fs.String("add", "", "add these skill ids")
	remove := fs.String("remove", "", "remove these skill ids")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}

	switch {
	case *set != "":
		ids, err := intList(*set)
		if err != nil {
			return err
		}
		if err := client.SetSkills(ctx, ids); err != nil {
			return err
		}
	case *add != "":
		ids, err := intList(*add)
		if err != nil {
			return err
		}
		if err := client.AddSkills(ctx, ids); err != nil {
			return err
		}
	case *remove != "":
		ids, err := intList(*remove)
		if err != nil {
			return err
		}
		if err := client.RemoveSkills(ctx, ids); err != nil {
			return err
		}
	}

	self, err := client.Self(ctx)
	if err != nil {
		return err
	}
	profile, err := client.Profile(ctx, self.ID)
	if err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, profile.Jobs)
	}
	for _, job := range profile.Jobs {
		category := ""
		if job.Category != nil {
			category = job.Category.Name
		}
		fmt.Fprintf(e.stdout, "%-8d %-28s %s\n", job.ID, job.Name, category)
	}
	return nil
}

func profileAvatar(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile avatar")
	file := fs.String("file", "", "image file to upload")
	x := fs.Int("x", 0, "crop offset x")
	y := fs.Int("y", 0, "crop offset y")
	width := fs.Int("width", 0, "crop width in pixels")
	height := fs.Int("height", 0, "crop height in pixels")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *file == "" || *width <= 0 || *height <= 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer profile avatar --file cover.png --width 400 --height 400 [--x 0 --y 0]")
		return ErrUsage
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := client.UploadProfilePicture(ctx, *file, data, *x, *y, *width, *height)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func profileRole(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile role")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(e.stderr, "usage: freelancer profile role <freelancer|employer>")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	if err := client.SetChosenRole(ctx, rest[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "chosen role set to %s\n", rest[0])
	return nil
}

func profileCurrency(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile currency")
	id := fs.Int64("id", 0, "currency id (see `freelancer currencies`)")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	if *id == 0 {
		fmt.Fprintln(e.stderr, "usage: freelancer profile currency --id 1")
		return ErrUsage
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	if err := client.SetPrimaryCurrency(ctx, *id); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "primary currency set to id %d\n", *id)
	return nil
}

func profileCV(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile cv")
	section := fs.String("section", "", "experience, education, publication, or certification")
	list := fs.Bool("list", false, "list existing entries in this section")
	user := fs.Int64("user", 0, "user id for --list (default: yourself)")
	add := fs.String("add", "", "JSON payload of the new entry")
	update := fs.Int64("update", 0, "entry id to update (with --add payload)")
	remove := fs.Int64("delete", 0, "entry id to delete")
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, "usage: freelancer profile cv --section experience --list")
		fmt.Fprintln(e.stderr, `       freelancer profile cv --section experience --add '{"title":"Backend Engineer","company":"Acme","start_date":"2021-03","end_date":"present"}'`)
		fmt.Fprintln(e.stderr, "       freelancer profile cv --section education --delete 123")
		fmt.Fprintln(e.stderr, "\ndates accept YYYY-MM, YYYY-MM-DD, epoch seconds, or \"present\" for an ongoing role")
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	kind, err := freelancer.ParseCVEntryKind(*section)
	if err != nil {
		return err
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	switch {
	case *list:
		raw, err := client.ListCVEntries(ctx, kind, *user, 50)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	case *remove != 0:
		if err := client.DeleteCVEntry(ctx, kind, *remove); err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "deleted %s %d\n", kind, *remove)
		return nil
	case *add != "":
		payload, err := decodeJSONObject(*add)
		if err != nil {
			return err
		}
		if *update != 0 {
			raw, err := client.UpdateCVEntry(ctx, kind, *update, payload)
			if err != nil {
				return err
			}
			return writeRaw(e, raw)
		}
		raw, err := client.AddCVEntry(ctx, kind, payload)
		if err != nil {
			return err
		}
		return writeRaw(e, raw)
	default:
		fmt.Fprintf(e.stderr, "%s requires: %v\n", kind, kind.RequiredFields())
		fs.Usage()
		return ErrUsage
	}
}

func profileSchools(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile schools")
	country := fs.String("country", "ID", "country code, e.g. ID or US")
	query := fs.String("query", "", "filter by name substring")
	limit := fs.Int("limit", 20, "maximum rows")
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, "usage: freelancer profile schools --country ID --query komputer")
		fmt.Fprintln(e.stderr, "education entries need the school_id this prints; a plain school name is dropped by the API")
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	schools, err := client.Schools(ctx, *country, *query)
	if err != nil {
		return err
	}
	if *limit > 0 && len(schools) > *limit {
		schools = schools[:*limit]
	}
	if e.jsonOut {
		return writeJSON(e, schools)
	}
	for _, school := range schools {
		fmt.Fprintf(e.stdout, "%-8d %s\n", school.ID, school.Name)
	}
	fmt.Fprintf(e.stdout, "\n%d schools\n", len(schools))
	return nil
}

func profilePortfolio(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile portfolio")
	user := fs.Int64("user", 0, "user id (default: yourself)")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	var ids []int64
	if *user != 0 {
		ids = []int64{*user}
	}
	raw, err := client.Portfolios(ctx, ids)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func profileReputation(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "profile reputation")
	user := fs.Int64("user", 0, "user id (default: yourself)")
	role := fs.String("role", "freelancer", "freelancer or employer")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	target := *user
	if target == 0 {
		target = client.UserID()
	}
	raw, err := client.Reputation(ctx, []int64{target}, *role)
	if err != nil {
		return err
	}
	return writeRaw(e, raw)
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
