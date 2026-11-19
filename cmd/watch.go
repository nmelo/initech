package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/watch"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Hand a wait to initech: it tells you when a condition holds, or when its deadline passes",
	Long: `A watch is a wait initech keeps for you, with a reason and a deadline. When
its condition becomes true, initech puts one line in your pane:
  [from initech, watch w12] FIRED: <why> (<what it saw>)
and when the deadline passes first:
  [from initech, watch w12] MISSED: <why> (deadline 14:30 passed)
Watches are kept across a restart of your pane and of the rig. Your super can
list every watch; nobody cancels another agent's watch.

  initech watch set --bead ini-abc --status ready_for_qa --why '<line>' --until 2h
  initech watch set --bead ini-abc --comment-by lead1 --why '<line>' --until 16:00
  initech watch set --to eng1 --receipt r7 --delivered --why '<line>' --until 30m
  initech watch set --to eng1 --receipt r7 --held-for 15m --why '<line>' --until 1h
  initech watch set --why '<line>' --until 12:00          A plain deadline
  initech watch list [--all]
  initech watch cancel w12

Every refusal exits 1 with one line and sets nothing. Against an initech
without watches: "older initech running; restart it for watches", exit 2.`,
}

var (
	watchBead, watchStatus, watchCommentBy      string
	watchTo, watchReceipt, watchHeldFor         string
	watchWhy, watchUntil                        string
	watchDelivered, watchHoldIfAsleep, watchAll bool
)

// watchNow is the clock watch set resolves --until against; tests set it.
var watchNow = time.Now

var watchSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set a watch: one condition (or none, for a plain deadline), a reason and a deadline",
	Long: `Sets a watch and prints its id. One condition, or none for a plain deadline:
  --bead <id> --status <status>      the bead reaches that status (one bd knows)
  --bead <id> --comment-by <agent>   a comment on the bead by that author
  --to <agent> --receipt <id> --delivered
                                     your held send to that agent is delivered
  --to <agent> --receipt <id> --held-for <duration>
                                     that send is still held after the duration
                                     (shorter than --until)
--why is the line you get back, one line, at most 200 characters; it must make
sense after you have been cleared. --until is the deadline in local time: a
duration (45m, 2h), a clock time today (14:30; a time already past is refused,
never moved to tomorrow) or an RFC 3339 time. --hold-if-asleep keeps the line
until you wake instead of waking you. Run it from your agent's pane: the
watch's owner is the pane that runs the command.`,
	Args: cobra.NoArgs,
	RunE: runWatchSet,
}

var watchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your watches; --all lists every watch in the rig (super or the operator)",
	Args:  cobra.NoArgs,
	RunE:  runWatchList,
}

var watchCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel one of your own watches",
	Args:  cobra.ExactArgs(1),
	RunE:  runWatchCancel,
}

func init() {
	f := watchSetCmd.Flags()
	f.StringVar(&watchBead, "bead", "", "The bead the condition is about")
	f.StringVar(&watchStatus, "status", "", "With --bead: fire when the bead reaches this status")
	f.StringVar(&watchCommentBy, "comment-by", "", "With --bead: fire on a comment by this author")
	f.StringVar(&watchTo, "to", "", "With --receipt: the agent your send went to")
	f.StringVar(&watchReceipt, "receipt", "", "With --to: the receipt your held send printed")
	f.BoolVar(&watchDelivered, "delivered", false, "With --to and --receipt: fire when the send is delivered")
	f.StringVar(&watchHeldFor, "held-for", "", "With --to and --receipt: fire if the send is still held after this duration")
	f.StringVar(&watchWhy, "why", "", "The line you get back (required, one line, at most 200 characters)")
	f.StringVar(&watchUntil, "until", "", "The deadline: a duration, a clock time today, or RFC 3339 (required)")
	f.BoolVar(&watchHoldIfAsleep, "hold-if-asleep", false, "If you are suspended when it ends, hold the line until you wake")
	watchListCmd.Flags().BoolVar(&watchAll, "all", false, "Every watch in the rig (super, or the operator outside any pane)")
	watchCmd.AddCommand(watchSetCmd, watchListCmd, watchCancelCmd)
	rootCmd.AddCommand(watchCmd)
}

// olderTUIWatchLine is what watch prints against an initech without watches.
const olderTUIWatchLine = "older initech running; restart it for watches"

// watchRequest builds watch set's request from its flags, refusing here
// what needs no initech to judge: the mandatory parts, the deadline, and a
// condition that is half given or given twice.
func watchRequest(now time.Time) (tui.WatchSetRequest, error) {
	var req tui.WatchSetRequest
	why := strings.TrimSpace(watchWhy)
	switch {
	case why == "":
		return req, errors.New("a watch needs --why: the line you get back when it ends")
	case strings.ContainsAny(watchWhy, "\r\n"):
		return req, errors.New("--why is one line: it is the line you get back")
	case len([]rune(why)) > watch.MaxWhy:
		return req, fmt.Errorf("--why is %d characters; at most %d", len([]rune(why)), watch.MaxWhy)
	case watchUntil == "":
		return req, errors.New("a watch needs --until: a duration, a clock time today, or an RFC 3339 time")
	}
	deadline, err := parseWatchUntil(watchUntil, now)
	if err != nil {
		return req, err
	}
	req.Why, req.Deadline, req.HoldIfAsleep = why, deadline, watchHoldIfAsleep

	beadCond := watchBead != "" || watchStatus != "" || watchCommentBy != ""
	sendCond := watchTo != "" || watchReceipt != "" || watchDelivered || watchHeldFor != ""
	switch {
	case beadCond && sendCond:
		return req, errors.New("one condition per watch: a bead condition or a send condition, not both")
	case beadCond:
		switch {
		case watchBead == "":
			return req, errors.New("--status and --comment-by go with --bead")
		case (watchStatus == "") == (watchCommentBy == ""):
			return req, errors.New("--bead takes one of --status or --comment-by")
		case watchStatus != "":
			req.Kind, req.Params = watch.KindBeadStatus, map[string]string{"bead": watchBead, "status": watchStatus}
		default:
			req.Kind, req.Params = watch.KindBeadComment, map[string]string{"bead": watchBead, "author": watchCommentBy}
		}
	case sendCond:
		switch {
		case watchTo == "" || watchReceipt == "":
			return req, errors.New("--to and --receipt go together: the agent your send went to and the receipt it printed")
		case watchDelivered == (watchHeldFor != ""):
			return req, errors.New("a send condition takes one of --delivered or --held-for")
		case watchDelivered:
			req.Kind, req.Params = watch.KindSendDelivered, map[string]string{"pane": watchTo, "receipt": watchReceipt}
		default:
			d, err := time.ParseDuration(watchHeldFor)
			if err != nil || d <= 0 {
				return req, fmt.Errorf("--held-for %q is not a duration such as 15m", watchHeldFor)
			}
			if !now.Add(d).Before(deadline) {
				return req, fmt.Errorf("--held-for %s is not shorter than --until: the deadline comes first", watchHeldFor)
			}
			req.Kind, req.Params = watch.KindSendHeld, map[string]string{"pane": watchTo, "receipt": watchReceipt, "for": d.String()}
		}
	default:
		req.Kind = watch.KindDeadline
	}
	return req, nil
}

// parseWatchUntil reads --until in local time: a duration from now, a clock
// time today (HH:MM), or RFC 3339. A time that is not after now is refused;
// a clock time already past is never moved to tomorrow.
func parseWatchUntil(s string, now time.Time) (time.Time, error) {
	var t time.Time
	if d, err := time.ParseDuration(s); err == nil {
		t = now.Add(d)
	} else if c, err := time.ParseInLocation("15:04", s, now.Location()); err == nil {
		t = time.Date(now.Year(), now.Month(), now.Day(), c.Hour(), c.Minute(), 0, 0, now.Location())
	} else if r, err := time.Parse(time.RFC3339, s); err == nil {
		t = r
	} else {
		return time.Time{}, fmt.Errorf("--until %q is not a duration (45m), a clock time today (14:30) or an RFC 3339 time", s)
	}
	if !t.After(now) {
		return time.Time{}, fmt.Errorf("--until %s is already past: a watch needs a deadline ahead", t.Local().Format("15:04"))
	}
	return t, nil
}

// watchCall sends a watch request and maps the answers: an initech that does
// not know the action is the older-initech line with exit 2; any other
// refusal is its one line with exit 1.
func watchCall(cmd *cobra.Command, req tui.IPCRequest) (*tui.IPCResponse, error) {
	cmd.SilenceUsage = true
	resp, err := ipcCall(req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "unknown action") {
			return nil, &olderTUIError{line: olderTUIWatchLine}
		}
		return nil, errors.New(resp.Error)
	}
	return resp, nil
}

func runWatchSet(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	req, err := watchRequest(watchNow())
	if err != nil {
		return err
	}
	body, _ := json.Marshal(req)
	resp, err := watchCall(cmd, tui.IPCRequest{Action: "watch_set", Text: string(body)})
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(resp.Data))
	return nil
}

func runWatchList(cmd *cobra.Command, _ []string) error {
	body, _ := json.Marshal(tui.WatchListRequest{All: watchAll})
	resp, err := watchCall(cmd, tui.IPCRequest{Action: "watch_list", Text: string(body)})
	if err != nil {
		return err
	}
	var rows []tui.WatchListing
	if err := json.Unmarshal([]byte(resp.Data), &rows); err != nil {
		return fmt.Errorf("reading the watch list: %w", err)
	}
	printWatchList(cmd.OutOrStdout(), rows, watchAll, watchNow())
	return nil
}

func runWatchCancel(cmd *cobra.Command, args []string) error {
	resp, err := watchCall(cmd, tui.IPCRequest{Action: "watch_cancel", Target: args[0]})
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(resp.Data))
	return nil
}

// printKeptWatches is the line initech clear and restart add after they
// succeed: a pane's watches are kept, and how many is said so none is a
// silent orphan. Nothing is printed for none, or when initech cannot say
// (an older initech, a window that keeps no watches): the clear or restart
// stands as it was.
func printKeptWatches(w io.Writer, agent string) {
	resp, err := ipcCall(tui.IPCRequest{Action: "watch_count", Target: agent})
	if err != nil || !resp.OK {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Data))
	if err != nil || n == 0 {
		return
	}
	noun := "watches"
	if n == 1 {
		noun = "watch"
	}
	fmt.Fprintf(w, "kept %d %s for %s\n", n, noun, agent)
}

// watchCondition is a watch's condition in the words list shows.
func watchCondition(w watch.Watch) string {
	p := w.Params
	switch w.Kind {
	case watch.KindBeadStatus:
		return fmt.Sprintf("%s reaches %s", p["bead"], p["status"])
	case watch.KindBeadComment:
		return fmt.Sprintf("a comment on %s by %s", p["bead"], p["author"])
	case watch.KindDeadline:
		return "the deadline"
	case watch.KindSendDelivered:
		return fmt.Sprintf("my send to %s (%s) delivered", p["pane"], p["receipt"])
	case watch.KindSendHeld:
		return fmt.Sprintf("my send to %s (%s) still held after %s", p["pane"], p["receipt"], p["for"])
	}
	return "unknown condition"
}

// printWatchList writes one row per watch: id, owner (with --all), state,
// deadline, condition, why, and the notes (the other owners sharing its
// check, and whether its line will wake a sleeping owner).
func printWatchList(w io.Writer, rows []tui.WatchListing, all bool, now time.Time) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no watches")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		cols := []string{r.ID}
		if all {
			cols = append(cols, r.Owner)
		}
		deadline := r.Deadline.Local().Format("15:04")
		if r.Deadline.Local().YearDay() != now.Local().YearDay() || r.Deadline.Year() != now.Year() {
			deadline = r.Deadline.Local().Format("Jan 2 15:04")
		}
		cols = append(cols, string(r.State), "until "+deadline, watchCondition(r.Watch), r.Why)
		var notes []string
		if len(r.Sharers) > 0 {
			notes = append(notes, "shared with "+strings.Join(r.Sharers, ", "))
		}
		if r.Wakes {
			notes = append(notes, "wakes")
		}
		if len(notes) > 0 {
			cols = append(cols, "("+strings.Join(notes, "; ")+")")
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	tw.Flush()
}
