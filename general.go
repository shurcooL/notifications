package notifications

// TODO: Consider best (centralized?) place for RepoSpec?

// RepoSpec is a specification for a repository.
type RepoSpec struct {
	URI string // URI is clean '/'-separated URI. E.g., "user/repo".
}

// String implements fmt.Stringer.
func (rs RepoSpec) String() string {
	return rs.URI
}
