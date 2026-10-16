package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func init() {
	register(Command{Name: "conversations", Summary: "list conversations with provider, turns, status and last activity", Run: runConversations})
}

// conversationView is one conversation as the listing reports it. Status and
// resumable are derived from the stored record.
type conversationView struct {
	ConversationID string  `json:"conversation_id"`
	Provider       string  `json:"provider"`
	Turns          int     `json:"turns"`
	Status         string  `json:"status"`
	Resumable      bool    `json:"resumable"`
	ActiveRunID    *string `json:"active_run_id"`
	LastActivity   string  `json:"last_activity"`
}

func viewOfConversation(c store.Conversation) conversationView {
	status := "idle"
	if c.ActiveRunID != nil {
		status = "busy"
	}
	return conversationView{
		ConversationID: c.ConversationID, Provider: c.Provider, Turns: len(c.Turns), Status: status,
		Resumable: c.ProviderSessionID != nil, ActiveRunID: c.ActiveRunID, LastActivity: c.UpdatedAt,
	}
}

func runConversations(ctx *Context, args []string) int {
	var asJSON bool
	fset := flag.NewFlagSet("conversations", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.BoolVar(&asJSON, "json", false, "print one JSON object")
	ctx.JSON = wantsJSON(args)
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(ctx.Stderr, "usage: msgate conversations [--json]")
			return ExitOK
		}
		return ctx.Fail(ExitUsage, "%v", err)
	}
	ctx.JSON = asJSON
	if fset.NArg() != 0 {
		return ctx.Fail(ExitUsage, "conversations takes no arguments, got %q", fset.Arg(0))
	}

	home, err := store.ResolveHome(ctx.Getenv)
	if err != nil {
		return ctx.Fail(ExitInternal, "%v", err)
	}
	views, err := conversationViews(ctx, store.Open(home))
	if err != nil {
		return ctx.Fail(ExitInternal, "listing conversations: %v", err)
	}
	if asJSON {
		return printJSON(ctx, struct {
			Conversations []conversationView `json:"conversations"`
		}{views})
	}
	if len(views) == 0 {
		return ExitOK
	}
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CONVERSATION\tPROVIDER\tTURNS\tSTATUS\tRESUMABLE\tLAST ACTIVITY")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%t\t%s\n", v.ConversationID, v.Provider, v.Turns, v.Status, v.Resumable, v.LastActivity)
	}
	tw.Flush()
	return ExitOK
}

// conversationViews lists every conversation, most recently active first.
func conversationViews(ctx *Context, st *store.Store) ([]conversationView, error) {
	ids, err := st.ConversationIDs()
	if err != nil {
		return nil, err
	}
	views := []conversationView{}
	for _, id := range ids {
		c, err := st.ReadConversation(id)
		if errors.Is(err, os.ErrNotExist) {
			continue // removed since the listing
		}
		if err != nil {
			ctx.Warnf("skipping conversation %s: %v", id, err)
			continue
		}
		views = append(views, viewOfConversation(c))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].LastActivity != views[j].LastActivity {
			return views[i].LastActivity > views[j].LastActivity
		}
		return views[i].ConversationID > views[j].ConversationID
	})
	return views, nil
}
