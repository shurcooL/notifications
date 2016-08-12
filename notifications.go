// Package notifications provides a notifications service definition.
package notifications

import (
	"fmt"
	"html/template"
	"time"

	"github.com/shurcooL/users"
	"golang.org/x/net/context"
)

// Service for notifications.
type Service interface {
	List(ctx context.Context, opt interface{}) (Notifications, error)
	Count(ctx context.Context, opt interface{}) (uint64, error)

	// MarkAllRead marks all notifications in the specified repository as read.
	MarkAllRead(ctx context.Context, repo RepoSpec) error

	ExternalService
}

// ExternalService for notifications.
type ExternalService interface {
	Subscribe(ctx context.Context, appID string, repo RepoSpec, threadID uint64, subscribers []users.UserSpec) error

	// MarkRead marks the specified thread as read.
	MarkRead(ctx context.Context, appID string, repo RepoSpec, threadID uint64) error

	Notify(ctx context.Context, appID string, repo RepoSpec, threadID uint64, nr NotificationRequest) error
}

// Notification represents a notification.
type Notification struct {
	AppID     string
	RepoSpec  RepoSpec
	ThreadID  uint64
	RepoURL   template.URL
	Title     string
	Icon      OcticonID // TODO: Some notifications can exist for a long time. OcticonID may change when frontend updates to newer versions of octicons. Think of a better long term solution?
	Color     RGB
	Actor     users.User
	UpdatedAt time.Time
	HTMLURL   template.URL // Address of notification target.
}

// NotificationRequest represents a request to create a notification.
type NotificationRequest struct {
	Title     string
	Icon      OcticonID
	Color     RGB
	Actor     users.UserSpec // Actor that triggered the notification.
	UpdatedAt time.Time      // TODO: Maybe not needed? Why not use time.Now()?
	HTMLURL   template.URL   // Address of notification target.
}

// Octicon ID. E.g., "issue-opened".
type OcticonID string

// RGB represents a 24-bit color without alpha channel.
type RGB struct {
	R, G, B uint8
}

// HexString returns a hexadecimal color string. For example, "#ff0000" for red.
func (c RGB) HexString() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Notifications implements sort.Interface.
type Notifications []Notification

func (s Notifications) Len() int           { return len(s) }
func (s Notifications) Less(i, j int) bool { return !s[i].UpdatedAt.Before(s[j].UpdatedAt) }
func (s Notifications) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
