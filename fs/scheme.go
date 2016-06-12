package fs

import (
	"fmt"
	"html/template"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
)

// repoSpec is an on-disk representation of notifications.RepoSpec.
type repoSpec struct {
	URI string
}

func fromRepoSpec(rs notifications.RepoSpec) repoSpec {
	return repoSpec{URI: rs.URI}
}

func (rs repoSpec) RepoSpec() notifications.RepoSpec {
	return notifications.RepoSpec{URI: rs.URI}
}

func marshalUserSpec(us users.UserSpec) string {
	return fmt.Sprintf("%d@%s", us.ID, us.Domain)
}

func unmarshalUserSpec(userSpec string) (users.UserSpec, error) {
	parts := strings.SplitN(userSpec, "@", 2)
	if len(parts) != 2 {
		return users.UserSpec{}, fmt.Errorf("user spec is not 2 parts:", len(parts))
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return users.UserSpec{}, err
	}
	return users.UserSpec{ID: id, Domain: parts[1]}, nil
}

// octiconID is an on-disk representation of notifications.OcticonID.
type octiconID string

func fromOcticonID(o notifications.OcticonID) octiconID {
	return octiconID(o)
}

func (o octiconID) OcticonID() notifications.OcticonID {
	return notifications.OcticonID(o)
}

type rgb struct {
	R, G, B uint8
}

func fromRGB(c notifications.RGB) rgb {
	return rgb{R: c.R, G: c.G, B: c.B}
}

func (c rgb) RGB() notifications.RGB {
	return notifications.RGB{R: c.R, G: c.G, B: c.B}
}

// notification is an on-disk representation of notifications.Notification.
type notification struct {
	RepoSpec  repoSpec
	Title     string
	Icon      octiconID
	Color     rgb
	UpdatedAt time.Time
	HTMLURL   template.URL
}

// Tree layout:
//
// 	root
// 	├── notifications
// 	│   └── userSpec
// 	│       └── domain.com-path-appID-threadID - encoded notification
// 	└── subscribers
// 	    └── domain.com
// 	        └── path
// 	            └── appID-threadID
// 	                └── userSpec - blank file

func notificationsDir(user users.UserSpec) string {
	return path.Join("notifications", marshalUserSpec(user))
}

func notificationPath(user users.UserSpec, key string) string {
	return path.Join("notifications", marshalUserSpec(user), key)
}

func notificationKey(repo notifications.RepoSpec, appID string, threadID uint64) string {
	if fmt.Sprintf("%s-%s-%d", repo.URI, appID, threadID) != repo.URI+"-"+appID+"-"+formatUint64(threadID) {
		panic("mismatch")
	}
	// TODO: Think about repo.URI replacement of "/" -> "-", is it optimal?
	return fmt.Sprintf("%s-%s-%d", strings.Replace(repo.URI, "/", "-", -1), appID, threadID)
}

func subscribersDir(repo notifications.RepoSpec, appID string, threadID uint64) string {
	return path.Join("subscribers", repo.URI, fmt.Sprintf("%s-%d", appID, threadID))
}

func subscriberPath(repo notifications.RepoSpec, appID string, threadID uint64, subscriber users.UserSpec) string {
	return path.Join("subscribers", repo.URI, fmt.Sprintf("%s-%d", appID, threadID), marshalUserSpec(subscriber))
}

// TODO: Sort out userSpec.
//
// userSpec is an on-disk representation of a specification for a user.
// It takes the form of "ID@Domain", e.g., "1@example.com".
/*type userSpec struct {
	Name string
}

func fromUserSpec(us users.UserSpec) userSpec {
	return userSpec{fmt.Sprintf("%d@%s", us.ID, us.Domain)}
}*/

/*func marshalUserSpec(us users.UserSpec) string {
	return fromUserSpec(us).Name
}*/

/*// UserSpec returns a zero user if there's an error parsing.
func (us userSpec) UserSpec() users.UserSpec {
	...
}*/

/*func (us userSpec) UserSpec() (users.UserSpec, error) {
	parts := strings.SplitN(string(us), "@", 2)
	if len(parts) != 2 {
		return users.UserSpec{}, fmt.Errorf("user spec is not 2 parts:", len(parts))
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return users.UserSpec{}, err
	}
	return users.UserSpec{ID: id, Domain: parts[1]}, nil
}*/
