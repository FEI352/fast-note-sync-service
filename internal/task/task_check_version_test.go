package task

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/fileurl"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceCheckNeverFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"failed", ``, 503}, {"empty", `[]`, 200},
		{"valid", `[{"tag_name":"v999.0.0","body":"fork release","assets":[{"name":"fast-note-sync-service-999.0.0-linux-amd64.tar.gz","state":"uploaded"}]}]`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			calls := 0
			http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != fileurl.ServiceReleaseAPI {
					t.Fatalf("unexpected service source: %s", r.URL)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(tc.body, "fast-note-sync-service-999.0.0-linux-amd64.tar.gz", serviceTestAsset("v999.0.0")))), Header: make(http.Header)}, nil
			})
			task := &CheckVersionTask{app: app.NewTestApp(nil)}
			// Even the legacy helper's explicit CNB choice must use our service repository.
			usedGitHub, releases, link, _, _, err := task.fetchReleasesWithFallback(context.Background(), "https://official.invalid", "https://cnb.cool/official", "", true, false)
			if calls != 1 || !usedGitHub {
				t.Fatalf("calls=%d usedGitHub=%v", calls, usedGitHub)
			}
			if tc.status != 200 && err == nil {
				t.Fatal("API failure was hidden")
			}
			if tc.name == "valid" {
				if len(releases) != 1 || releases[0].Tag != "v999.0.0" || link != fileurl.ServiceRepoURL+"/releases/tag/v999.0.0" {
					t.Fatalf("unexpected release result: %+v %s", releases, link)
				}
			}
		})
	}
}

func TestServiceReleaseFiltering(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(`[
   {"tag_name":"../../evil","assets":[{"name":"TEST_ASSET"}]},
   {"tag_name":"3.0.0-rc.1","assets":[{"name":"TEST_ASSET"}]},
   {"tag_name":"3.0.0","draft":true,"assets":[{"name":"TEST_ASSET"}]},
   {"tag_name":"2.0.0","assets":[{"name":"TEST_ASSET"}]}
  ]`, "TEST_ASSET", serviceTestAsset("2.0.0")))), Header: make(http.Header)}, nil
	})
	a := app.NewTestApp(nil)
	a.Config().App.PullReleaseChannel = "stable"
	task := &CheckVersionTask{app: a}
	releases, err := task.fetchGitHubReleasesCtx(context.Background(), fileurl.ServiceReleaseAPI)
	if err != nil || len(releases) != 1 || releases[0].Version != "v2.0.0" {
		t.Fatalf("unexpected filtered releases: %+v %v", releases, err)
	}
}

func TestServiceCheckRunIgnoresPullSource(t *testing.T) {
	for _, mode := range []string{"auto", "github", "cnb"} {
		t.Run(mode, func(t *testing.T) {
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			serviceCalls := 0
			http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
				body := `[]`
				switch r.URL.String() {
				case fileurl.ServiceReleaseAPI:
					serviceCalls++
					body = `[{"tag_name":"v999.0.0","assets":[{"name":"fast-note-sync-service-v999.0.0-linux-amd64.tar.gz"}]}]`
				case GitHubPluginReleaseURL, CNBPluginReleaseURL:
				default:
					t.Fatalf("unexpected source/probe: %s", r.URL)
				}
				body = strings.ReplaceAll(body, "fast-note-sync-service-v999.0.0-linux-amd64.tar.gz", serviceTestAsset("v999.0.0"))
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			a := app.NewTestApp(nil)
			a.Config().App.PullSource = mode
			task := &CheckVersionTask{app: a}
			if err := task.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			info := a.CheckVersion("")
			if serviceCalls != 1 || !info.GithubAvailable || info.VersionNewTag != "v999.0.0" || info.VersionNewLink != fileurl.ServiceRepoURL+"/releases/tag/v999.0.0" {
				t.Fatalf("unexpected service check: %+v calls=%d", info, serviceCalls)
			}
		})
	}
}

func serviceTestAsset(tag string) string {
	_, name, err := fileurl.ServiceDownload(tag, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		panic(fmt.Sprint(err))
	}
	return name
}

func TestServiceHistoricalTags(t *testing.T) {
	a := app.NewTestApp(nil)
	a.SetCheckVersionReleases([]pkgapp.HistoricalVersion{{Version: "v3.6.1", Tag: "v3.6.1"}, {Version: "v3.5.0", Tag: "3.5.0"}}, nil)
	for version, tag := range map[string]string{"3.6.1": "v3.6.1", "3.5.0": "3.5.0", "3.4.0": "v3.4.0"} {
		if got := a.ServiceReleaseTag(version); got != tag {
			t.Errorf("%s: got %s want %s", version, got, tag)
		}
	}
}
