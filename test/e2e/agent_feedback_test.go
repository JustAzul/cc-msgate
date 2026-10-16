package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

func agentFeedbackOf(t *testing.T, s *sandbox, runID string) (request, status any) {
	t.Helper()
	req := readJSONFile(t, filepath.Join(s.home, "runs", runID, "request.json"))
	st := s.run("status", runID, "--json")
	if st.code != 0 {
		t.Fatalf("status exit %d %s", st.code, st.stderr)
	}
	return req["agent_feedback"], st.json(t)["agent_feedback"]
}

func TestAgentFeedbackIsRecordedForARunWithASession(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *sandbox) *sandbox
		args  []string
	}{
		{"flag", func(s *sandbox) *sandbox { return s }, []string{"--agent-feedback", "--session-id", "sess-a"}},
		{"env", func(s *sandbox) *sandbox { return s.set("MSGATE_AGENT_FEEDBACK", "1") }, []string{"--session-id", "sess-a"}},
		{"session from the environment", func(s *sandbox) *sandbox { return s.set("CLAUDE_CODE_SESSION_ID", "sess-a") }, []string{"--agent-feedback"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.setup(newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")))
			r := s.run(append([]string{"exec", "--run-id", "af1"}, append(c.args, "q")...)...)
			if r.code != 0 {
				t.Fatalf("exit %d %s", r.code, r.stderr)
			}
			request, status := agentFeedbackOf(t, s, "af1")
			if request != true || status != true {
				t.Errorf("agent_feedback: request %v, status %v; want true, true", request, status)
			}
			if strings.Contains(r.stderr, "agent-feedback") {
				t.Errorf("unexpected note on stderr: %q", r.stderr)
			}
		})
	}
}

func TestAgentFeedbackWithoutASessionRunsAndSaysWhyItIsOff(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok"))

	r := s.run("exec", "--run-id", "af2", "--agent-feedback", "q")

	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "--agent-feedback ignored: no session id") {
		t.Errorf("stderr = %q, want the ignored note", r.stderr)
	}
	request, status := agentFeedbackOf(t, s, "af2")
	if request != false || status != false {
		t.Errorf("agent_feedback: request %v, status %v; want false, false", request, status)
	}
}

func TestAgentFeedbackIsOffByDefault(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("exec-ok")).set("MSGATE_AGENT_FEEDBACK", "0")

	r := s.run("exec", "--run-id", "af3", "--session-id", "sess-a", "q")

	if r.code != 0 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	request, status := agentFeedbackOf(t, s, "af3")
	if request != false || status != false {
		t.Errorf("agent_feedback: request %v, status %v; want false, false", request, status)
	}
}

func TestAgentFeedbackAppliesToReviewAndSend(t *testing.T) {
	s := newSandbox(t).set("FAKECODEX_FIXTURE", fixture("review-ok"))
	repo := gitRepo(t)
	if r := s.run("review", "--uncommitted", "--cwd", repo, "--run-id", "af4", "--agent-feedback", "--session-id", "sess-a"); r.code != 0 {
		t.Fatalf("review exit %d %s", r.code, r.stderr)
	}
	if request, _ := agentFeedbackOf(t, s, "af4"); request != true {
		t.Errorf("review agent_feedback = %v, want true", request)
	}

	s.set("FAKECODEX_FIXTURE", fixture("resume-turn1"))
	if r := s.run("exec", "--cwd", repo, "--run-id", "af5", "q"); r.code != 0 {
		t.Fatalf("exec exit %d %s", r.code, r.stderr)
	}
	s.set("FAKECODEX_FIXTURE", fixture("resume-turn2"))
	if r := s.run("send", "af5", "--run-id", "af6", "--agent-feedback", "--session-id", "sess-a", "more"); r.code != 0 {
		t.Fatalf("send exit %d %s", r.code, r.stderr)
	}
	if request, _ := agentFeedbackOf(t, s, "af6"); request != true {
		t.Errorf("send agent_feedback = %v, want true", request)
	}
}
