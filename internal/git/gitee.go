package git

type GiteeProvider struct {
	remote string
}

func NewGiteeProvider(remote string) *GiteeProvider {
	return &GiteeProvider{remote: remote}
}

func (p *GiteeProvider) Name() string   { return "gitee" }
func (p *GiteeProvider) Remote() string { return p.remote }
