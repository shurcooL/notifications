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
	InternalService
	ExternalService
}

// InternalService for notifications.
type InternalService interface {
	List(ctx context.Context, opt interface{}) (Notifications, error)
	Count(ctx context.Context, opt interface{}) (uint64, error)
}

// ExternalService for notifications.
type ExternalService interface {
	Subscribe(ctx context.Context, appID string, repo RepoSpec, threadID uint64, subscribers []users.UserSpec) error

	MarkRead(ctx context.Context, appID string, repo RepoSpec, threadID uint64) error

	Notify(ctx context.Context, appID string, repo RepoSpec, threadID uint64, notification Notification) error
}

// Notification represents a notification.
type Notification struct {
	RepoSpec  RepoSpec
	RepoURL   template.URL
	Title     string
	Icon      OcticonID
	Color     RGB
	UpdatedAt time.Time
	HTMLURL   template.URL // Address of notification target.
}

// Octicon ID. E.g., "issue-opened".
type OcticonID string

// RGB represents a 24-bit color without alpha channel.
type RGB struct {
	R, G, B uint8
}

// Hex returns a hexadecimal color string. For example, "#ff0000" for red.
func (c RGB) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Notifications implements sort.Interface.
type Notifications []Notification

func (s Notifications) Len() int           { return len(s) }
func (s Notifications) Less(i, j int) bool { return !s[i].UpdatedAt.Before(s[j].UpdatedAt) }
func (s Notifications) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
