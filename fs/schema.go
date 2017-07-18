package fs

import (
	"fmt"
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
	return repoSpec(rs)
}

func (rs repoSpec) RepoSpec() notifications.RepoSpec {
	return notifications.RepoSpec(rs)
}

func marshalUserSpec(us users.UserSpec) string {
	return fmt.Sprintf("%d@%s", us.ID, us.Domain)
}

// unmarshalUserSpec parses userSpec, a string like "1@example.com"
// into a users.UserSpec{ID: 1, Domain: "example.com"}.
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
	return rgb(c)
}

func (c rgb) RGB() notifications.RGB {
	return notifications.RGB(c)
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
	HTMLURL   string

	Participating bool
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
// 	            ├── appID-threadID
// 	            │   └── userSpec - blank file
// 	            └── userSpec - blank file
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
	return path.Join(notificationsDir(user), key)
}

func notificationKey(repo notifications.RepoSpec, appID string, threadID uint64) string {
	// TODO: Think about repo.URI replacement of "/" -> "-", is it optimal?
	return fmt.Sprintf("%s-%s-%d", strings.Replace(repo.URI, "/", "-", -1), appID, threadID)
}

func subscribersDir(repo notifications.RepoSpec, appID string, threadID uint64) string {
	switch {
	default:
		return path.Join("subscribers", repo.URI, fmt.Sprintf("%s-%d", appID, threadID))
	case appID == "" && threadID == 0:
		return path.Join("subscribers", repo.URI)
	}
}

func subscriberPath(repo notifications.RepoSpec, appID string, threadID uint64, subscriber users.UserSpec) string {
	return path.Join(subscribersDir(repo, appID, threadID), marshalUserSpec(subscriber))
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
