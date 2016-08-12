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
		return users.UserSpec{}, fmt.Errorf("user spec is not 2 parts: %v", len(parts))
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

// userSpec is an on-disk representation of users.UserSpec.
type userSpec struct {
	ID     uint64
	Domain string `json:",omitempty"`
}

func fromUserSpec(us users.UserSpec) userSpec {
	return userSpec{ID: us.ID, Domain: us.Domain}
}

func (us userSpec) UserSpec() users.UserSpec {
	return users.UserSpec{ID: us.ID, Domain: us.Domain}
}

func (us userSpec) Equal(other users.UserSpec) bool {
	return us.Domain == other.Domain && us.ID == other.ID
}

// rgb is an on-disk representation of notifications.RGB.
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
	AppID     string // TODO: Maybe deduce appID and threadID from fi.Name() rather than adding that to encoded JSON...
	RepoSpec  repoSpec
	ThreadID  uint64 // TODO: Maybe deduce appID and threadID from fi.Name() rather than adding that to encoded JSON...
	Title     string
	Icon      octiconID
	Color     rgb
	Actor     userSpec
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
//
// AppID is primarily needed to separate namespaces of {Repo, ThreadID}.
// Without AppID, a notification about issue 1 in repo "a" would clash
// with a notification of another type also with threadID 1 in repo "a".
//
// TODO: Consider renaming it to "ThreadType" and make it 2nd parameter,
//       so that it's more clear. (RepoURI, ThreadType, ThreadID).

func notificationsDir(user users.UserSpec) string {
	return path.Join("notifications", marshalUserSpec(user))
}

func notificationPath(user users.UserSpec, key string) string {
	return path.Join("notifications", marshalUserSpec(user), key)
}

func notificationKey(repo notifications.RepoSpec, appID string, threadID uint64) string {
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
