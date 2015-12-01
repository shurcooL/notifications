package notifications

import (
	"html/template"
	"time"

	"golang.org/x/net/context"
	"src.sourcegraph.com/apps/tracker/issues"
)

type Service interface {
	List(ctx context.Context, opt interface{}) ([]Notification, error)

	// TODO: This doesn't belong here, does it?
	//CurrentUser(ctx context.Context) (*User, error)
}

type CopierFrom interface {
	CopyFrom(src Service, repo issues.RepoSpec) error // TODO: Consider best place for RepoSpec?
}

type Notification struct {
	RepoSpec issues.RepoSpec

	Subject NotificationSubject

	UpdatedAt time.Time
}

type NotificationSubject struct {
	Title            string
	URL              string
	LatestCommentURL string
	Type             string

	// TODO.
	HTMLURL template.URL
	State   string
}
