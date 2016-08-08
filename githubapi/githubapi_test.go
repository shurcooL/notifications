package githubapi

import (
	"html/template"
	"testing"

	"github.com/google/go-github/github"
)

func TestGetCommitURL(t *testing.T) {
	url := "https://api.github.com/repos/owner/name/commits/63552f503fd0adeaf7401c40f7f24412e2e6aa6b"
	n := github.NotificationSubject{
		URL: &url,
	}
	got, err := getCommitURL(n)
	if err != nil {
		t.Fatal(err)
	}
	if want := template.URL("https://github.com/owner/name/commit/63552f503fd0adeaf7401c40f7f24412e2e6aa6b"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
