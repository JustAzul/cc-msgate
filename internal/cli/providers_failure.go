package cli

import (
	"fmt"
	"time"

	"github.com/JustAzul/cc-msgate/internal/provider"
	"github.com/JustAzul/cc-msgate/internal/store"
)

// markOnFailure is the hook a run runs when it ends failed: it marks prov
// unavailable in the cache when the error text says it cannot be used. A
// failure to write the mark only warns.
func markOnFailure(st *store.Store, prov provider.Provider, now func() time.Time, warn func(string)) func(string) {
	return func(errText string) {
		if err := markProviderDown(st, prov, errText, now()); err != nil {
			warn(fmt.Sprintf("could not mark provider %s unavailable: %v", prov.Name(), err))
		}
	}
}

// markProviderDown writes prov as unavailable until its next check when its
// classification of errText is auth, quota or rate-limit, through the same
// entry a providers mark writes. Any other class writes nothing.
func markProviderDown(st *store.Store, prov provider.Provider, errText string, now time.Time) error {
	reason := provider.ClassifyFailure(prov, errText).Reason()
	if reason == "" {
		return nil
	}
	entry, err := markEntry(reason, "", now)
	if err != nil {
		return err
	}
	name := prov.Name()
	return st.UpdateProviders(func(c *store.ProviderCache) { c.Providers[name] = entry })
}
