package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/holacloud/store"
)

// Owner is a user or organization of a git host and the list of its public
// repositories as of ListedAt.
type Owner struct {
	*store.Id `json:",inline"`

	Host      string `json:"host"`
	Login     string `json:"login"` // display case, as reported by the host
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"` // "User" | "Organization"
	AvatarURL string `json:"avatar_url,omitempty"`
	Bio       string `json:"bio,omitempty"`

	Repos     []OwnerRepo `json:"repos"`
	Truncated bool        `json:"truncated,omitempty"` // more repos than the configured limit

	CreatedAt time.Time `json:"created_at"`
	ListedAt  time.Time `json:"listed_at"`
}

// OwnerRepo is the summary of a repository in an owner listing. Forks and
// empty repositories are not listed.
type OwnerRepo struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Stars       int       `json:"stars"`
	Archived    bool      `json:"archived,omitempty"`
	PushedAt    time.Time `json:"pushed_at"`
	SizeKB      int64     `json:"size_kb"`
}

func (o *Owner) Slug() string {
	return o.Host + "/" + o.Login
}

// OwnerID builds the store id of an owner, see RepoID.
func OwnerID(host, login string) (string, error) {
	host = strings.ToLower(host)
	if !SupportedHosts[host] {
		return "", fmt.Errorf("host '%s' is not supported", host)
	}
	if !reOwner.MatchString(login) {
		return "", fmt.Errorf("invalid user or organization '%s'", login)
	}
	return strings.ToLower(host + "~" + login), nil
}
