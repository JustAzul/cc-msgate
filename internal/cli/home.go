package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/JustAzul/cc-msgate/internal/store"
)

// migrateHome moves the old home to the new one, once, before a command uses
// the home. A command that finds the new home never looks at the old one.
func migrateHome(ctx *Context, args []string) int {
	m, pending := store.PendingHomeMigration(ctx.Getenv)
	if !pending {
		return ExitOK
	}
	if err := moveOldHome(ctx, m); err != nil {
		ctx.JSON = wantsJSON(args)
		return ctx.Fail(ExitHomeMigration, "cannot move the old home %s to %s: %v", m.Old, m.New, err)
	}
	return ExitOK
}

// moveOldHome takes the migration lock, checks again that the move is still
// due, settles the old home's runs and renames it.
func moveOldHome(ctx *Context, m store.HomeMigration) error {
	unlock, err := m.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if !m.Pending() { // another process moved it while this one waited
		return nil
	}
	if err := requireSettledRuns(ctx, m.OldStore()); err != nil {
		return err
	}
	return m.Move()
}

// requireSettledRuns reconciles every run of the old home as status does and
// fails naming the first one that is still not terminal, so no live worker
// writes into a directory about to move.
func requireSettledRuns(ctx *Context, old *store.Store) error {
	ids, err := old.RunIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		state, err := observeRun(ctx, old, id)
		if errors.Is(err, os.ErrNotExist) {
			continue // a run directory with no state is not a run
		}
		if err != nil {
			return fmt.Errorf("run %s cannot be read: %w", id, err)
		}
		if !store.IsTerminal(state.State) {
			return fmt.Errorf("run %s is still %s", id, state.State)
		}
	}
	return nil
}
