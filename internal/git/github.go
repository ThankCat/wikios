package git

type GitHubProvider struct {
	remote string
}

func NewGitHubProvider(remote string) *GitHubProvider {
	return &GitHubProvider{remote: remote}
}

func (p *GitHubProvider) Name() string   { return "github" }
func (p *GitHubProvider) Remote() string { return p.remote }
