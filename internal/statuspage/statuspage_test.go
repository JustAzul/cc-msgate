package statuspage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/statuspage"
)

// summaryServing serves body as the status page's summary.json.
func summaryServing(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func summaryWith(responses string) string {
	return `{"status":{"indicator":"none"},"components":[` +
		`{"name":"Chat Completions","status":"operational"},` +
		`{"name":"Responses","status":"` + responses + `"},` +
		`{"name":"Login","status":"operational"}]}`
}

func TestMajorOutageOnAWatchedComponentIsDown(t *testing.T) {
	url := summaryServing(t, 200, summaryWith("major_outage"))

	down, err := statuspage.MajorOutage(context.Background(), url, "Login", "Responses")

	if err != nil || !down {
		t.Errorf("down, err = %v, %v; want true, nil", down, err)
	}
}

func TestALesserStatusOnAWatchedComponentIsNotAnOutage(t *testing.T) {
	for _, status := range []string{"operational", "degraded_performance", "partial_outage", "under_maintenance"} {
		url := summaryServing(t, 200, summaryWith(status))

		down, err := statuspage.MajorOutage(context.Background(), url, "Responses")

		if err != nil || down {
			t.Errorf("status %q: down, err = %v, %v; want false, nil", status, down, err)
		}
	}
}

func TestMajorOutageOnAnUnwatchedComponentIsNotDown(t *testing.T) {
	url := summaryServing(t, 200, summaryWith("operational"))

	down, err := statuspage.MajorOutage(context.Background(), url, "Responses", "Login")

	if err != nil || down {
		t.Errorf("down, err = %v, %v; want false, nil (Chat Completions is not watched)", down, err)
	}
}

func TestOffSwitchesTheLayerOffWithoutAnyRequest(t *testing.T) {
	_, err := statuspage.MajorOutage(context.Background(), "off", "Responses")

	if !errors.Is(err, statuspage.ErrOff) {
		t.Errorf("err = %v, want ErrOff", err)
	}
}

func TestAPageThatDoesNotAnswerIsUnreachableAndOneThatIsNotASummaryIsUnreadable(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	cases := []struct {
		name, url string
		want      error
	}{
		{"unreachable", closed.URL, statuspage.ErrUnreachable},
		{"http 500", summaryServing(t, 500, summaryWith("major_outage")), statuspage.ErrUnreachable},
		{"not json", summaryServing(t, 200, "<html>maintenance</html>"), statuspage.ErrUnreadable},
		{"no listing", summaryServing(t, 200, `{"status":{"indicator":"none"}}`), statuspage.ErrUnreadable},
	}
	for _, c := range cases {
		down, err := statuspage.MajorOutage(context.Background(), c.url, "Responses")

		if down || !errors.Is(err, c.want) {
			t.Errorf("%s: down, err = %v, %v; want false and %v", c.name, down, err, c.want)
		}
	}
}
