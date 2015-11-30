package notifications

import "src.sourcegraph.com/apps/tracker/issues"

type Service interface {
	// TODO.

	// TODO: This doesn't belong here, does it?
	//CurrentUser(ctx context.Context) (*User, error)
}

type CopierFrom interface {
	CopyFrom(src Service, repo issues.RepoSpec) error // TODO: Consider best place for RepoSpec?
}

// TODO.
