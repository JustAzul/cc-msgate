package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/store"
)

func TestResolveHome(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"msgate home wins", map[string]string{"MSGATE_HOME": "/a", "XDG_STATE_HOME": "/x", "HOME": "/h"}, "/a"},
		{"xdg", map[string]string{"XDG_STATE_HOME": "/x", "HOME": "/h"}, "/x/msgate"},
		{"default", map[string]string{"HOME": "/h"}, "/h/.local/state/msgate"},
		{"old home variable ignored", map[string]string{"AGENTCLI_HOME": "/old", "XDG_STATE_HOME": "/x", "HOME": "/h"}, "/x/msgate"},
		{"old home variable ignored without xdg", map[string]string{"AGENTCLI_HOME": "/old", "HOME": "/h"}, "/h/.local/state/msgate"},
	}
	for _, c := range cases {
		got, err := store.ResolveHome(env(c.env))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v want %q", c.name, got, err, c.want)
		}
	}
	if _, err := store.ResolveHome(env(nil)); err == nil {
		t.Error("expected error when nothing resolves")
	}
	if _, err := store.ResolveHome(env(map[string]string{"AGENTCLI_HOME": "/old"})); err == nil {
		t.Error("AGENTCLI_HOME alone must not resolve a home")
	}
	_, err := store.ResolveHome(env(nil))
	if err == nil || !strings.Contains(err.Error(), "MSGATE_HOME") {
		t.Errorf("error %v must name MSGATE_HOME", err)
	}
}

func TestGeneratedIDs(t *testing.T) {
	now := time.Date(2026, 10, 4, 23, 15, 0, 0, time.UTC)
	r := store.NewRunID(now)
	c := store.NewConversationID(now)
	m := store.NewCallID(now)
	if !regexp.MustCompile(`^r-20261004T231500Z-[0-9a-f]{8}$`).MatchString(r) {
		t.Errorf("run id %q", r)
	}
	if !regexp.MustCompile(`^c-20261004T231500Z-[0-9a-f]{8}$`).MatchString(c) {
		t.Errorf("conversation id %q", c)
	}
	if !regexp.MustCompile(`^m-20261004T231500Z-[0-9a-f]{8}$`).MatchString(m) {
		t.Errorf("call id %q", m)
	}
	if r == store.NewRunID(now) {
		t.Error("ids must differ")
	}
}

func TestValidateRunID(t *testing.T) {
	ok := []string{"ok.id-1", "a", "A_b", "x.", "r-1"}
	bad := []string{"", "a b", ".", "..", ".hidden", "-x", "a/b", "a\x00b", string(make([]byte, 129))}
	for _, s := range ok {
		if err := store.ValidateRunID(s); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := store.ValidateRunID(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	long := make([]byte, 128)
	for i := range long {
		long[i] = 'a'
	}
	if err := store.ValidateRunID(string(long)); err != nil {
		t.Errorf("128 chars rejected: %v", err)
	}
}

func TestCreateRunDirExclusiveAndPermissions(t *testing.T) {
	s := store.Open(filepath.Join(t.TempDir(), "home"))
	if err := s.CreateRunDir("r1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRunDir("r1"); !errors.Is(err, store.ErrRunExists) {
		t.Fatalf("second create: %v", err)
	}
	for _, d := range []string{s.Home, filepath.Join(s.Home, "runs"), s.RunDir("r1")} {
		st, err := os.Stat(d)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st, err)
		}
	}
	if err := s.WritePrompt("r1", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(s.RunDir("r1"), "prompt.md"))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("prompt mode %v", st.Mode())
	}
}

func TestStateRoundTripAndNulls(t *testing.T) {
	s := store.Open(t.TempDir())
	_ = s.CreateRunDir("r1")
	st := store.State{RunID: "r1", ConversationID: "c1", Turn: 1, State: "queued", AdmittedAt: "2026-10-04T23:15:00Z", OutputPath: "/o", RunDir: "/d"}
	if err := s.WriteState("r1", st); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadState("r1")
	// The recorded paths name the home the run was admitted in; a read locates
	// the run in the home it is opened from.
	located := st
	located.OutputPath, located.RunDir = s.OutputPath("r1"), s.RunDir("r1")
	if err != nil || !reflect.DeepEqual(got, located) {
		t.Fatalf("got %+v err %v", got, err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.RunDir("r1"), "state.json"))
	for _, recorded := range []string{`"output_path": "/o"`, `"run_dir": "/d"`} {
		if !strings.Contains(string(raw), recorded) {
			t.Errorf("state.json lacks %s:\n%s", recorded, raw)
		}
	}
	for _, want := range []string{`"started_at": null`, `"exit_code": null`, `"outcome": null`, `"error_excerpt": null`, `"unparsed_events": 0`, `"background": false`} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).Match(raw) {
			t.Errorf("state.json lacks %s:\n%s", want, raw)
		}
	}
}

func TestConversationCreateAndUpdate(t *testing.T) {
	s := store.Open(t.TempDir())
	c := store.Conversation{ConversationID: "c1", Provider: "codex", Cwd: "/w", Turns: []string{"r1"}, ActiveRunID: strPtr("r1"), CreatedAt: "t", UpdatedAt: "t"}
	if err := s.CreateConversation(c); err != nil {
		t.Fatal(err)
	}
	err := s.UpdateConversation("c1", func(c *store.Conversation) { c.ProviderSessionID = strPtr("T"); c.ActiveRunID = nil })
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadConversation("c1")
	if err != nil || got.ProviderSessionID == nil || *got.ProviderSessionID != "T" || got.ActiveRunID != nil {
		t.Fatalf("got %+v err %v", got, err)
	}
	st, _ := os.Stat(filepath.Join(s.Home, "conversations", "c1.json"))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func strPtr(s string) *string { return &s }

func TestFindByProviderSessionMatchesProviderAndSession(t *testing.T) {
	s := store.Open(t.TempDir())
	held := func(id, provider, session string) {
		c := newConversation(id)
		c.Provider, c.ProviderSessionID = provider, &session
		if err := s.CreateConversation(c); err != nil {
			t.Fatal(err)
		}
	}
	held("c1", "codex", "s1")
	held("c2", "other", "s2")
	if err := s.CreateConversation(newConversation("c3")); err != nil { // reports no session yet
		t.Fatal(err)
	}

	if got, err := s.FindByProviderSession("codex", "s1"); err != nil || got.ConversationID != "c1" {
		t.Errorf("codex s1 = %q, %v, want c1", got.ConversationID, err)
	}
	for _, c := range []struct{ provider, session string }{{"codex", "s2"}, {"other", "s1"}, {"codex", ""}, {"codex", "nope"}} {
		if _, err := s.FindByProviderSession(c.provider, c.session); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s %q: err = %v, want not-exist", c.provider, c.session, err)
		}
	}
}
