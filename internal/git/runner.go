package git

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	EnvWikiGitURL      = "WIKIOS_WIKI_GIT_URL"
	EnvWikiGitToken    = "WIKIOS_WIKI_GIT_TOKEN"
	EnvWikiGitUsername = "WIKIOS_WIKI_GIT_USERNAME"
)

type RunnerConfig struct {
	RepoDir  string
	URL      string
	Remote   string
	Branch   string
	Username string
	Token    string
	Timeout  time.Duration
}

type Runner struct {
	config RunnerConfig
}

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func ConfigFromEnv(repoDir string, remote string, branch string) RunnerConfig {
	return RunnerConfig{
		RepoDir:  repoDir,
		URL:      strings.TrimSpace(os.Getenv(EnvWikiGitURL)),
		Remote:   strings.TrimSpace(remote),
		Branch:   strings.TrimSpace(branch),
		Username: firstNonEmpty(strings.TrimSpace(os.Getenv(EnvWikiGitUsername)), "x-access-token"),
		Token:    strings.TrimSpace(os.Getenv(EnvWikiGitToken)),
		Timeout:  60 * time.Second,
	}
}

func NewRunner(config RunnerConfig) *Runner {
	if config.Username == "" {
		config.Username = "x-access-token"
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	return &Runner{config: config}
}

func (r *Runner) URL() string {
	return strings.TrimSpace(r.config.URL)
}

func (r *Runner) RedactedURL(value string) string {
	return RedactRemoteURL(r.redact(value))
}

func (r *Runner) AuthConfigured() bool {
	return r.AuthConfiguredFor("")
}

func (r *Runner) AuthConfiguredFor(remoteURL string) bool {
	urlValue := firstNonEmpty(r.config.URL, remoteURL)
	if urlValue == "" {
		return strings.TrimSpace(r.config.Token) != ""
	}
	if isHTTPRemote(urlValue) {
		if strings.TrimSpace(r.config.Token) != "" {
			return true
		}
		u, err := url.Parse(urlValue)
		return err == nil && u.User != nil
	}
	if isSSHRemote(urlValue) {
		return true
	}
	return true
}

func (r *Runner) Run(ctx context.Context, args ...string) (Result, error) {
	return r.RunAt(ctx, r.config.RepoDir, args...)
}

func (r *Runner) RunAt(ctx context.Context, cwd string, args ...string) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", args...)
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	env, cleanup, err := r.env(runCtx, cwd)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer cleanup()
	cmd.Env = env

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result := Result{
		Stdout:   r.redact(stdout.String()),
		Stderr:   r.redact(stderr.String()),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}
	if runCtx.Err() != nil {
		result.ExitCode = -1
		return result, runCtx.Err()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, err
}

func (r *Runner) RemoteURL(ctx context.Context, remote string) (string, bool) {
	remote = firstNonEmpty(remote, r.config.Remote)
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(r.config.RepoDir) == "" {
		return "", false
	}
	result, err := r.Run(ctx, "remote", "get-url", remote)
	if err != nil || result.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(result.Stdout), true
}

func (r *Runner) redact(value string) string {
	out := value
	for _, secret := range []string{r.config.Token} {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			out = strings.ReplaceAll(out, secret, "[redacted]")
		}
	}
	return RedactRemoteURL(out)
}

func (r *Runner) env(ctx context.Context, cwd string) ([]string, func(), error) {
	env := os.Environ()
	sshCommand := firstNonEmpty(envValue(env, "GIT_SSH_COMMAND"), configuredSSHCommand(ctx, cwd), "ssh")
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=/bin/false",
		"SSH_ASKPASS_REQUIRE=never",
		"GCM_INTERACTIVE=never",
		"GIT_SSH_COMMAND="+NonInteractiveSSHCommand(sshCommand),
	)

	if strings.TrimSpace(r.config.Token) == "" {
		env = append(env, "GIT_ASKPASS=/bin/false")
		return env, func() {}, nil
	}

	askpass, cleanup, err := createAskPassScript()
	if err != nil {
		return nil, func() {}, err
	}
	env = append(env,
		"GIT_ASKPASS="+askpass,
		"WIKIOS_GIT_ASKPASS_USERNAME="+r.config.Username,
		"WIKIOS_GIT_ASKPASS_TOKEN="+r.config.Token,
	)
	return env, cleanup, nil
}

func createAskPassScript() (string, func(), error) {
	dir, err := os.MkdirTemp("", "wikios-git-askpass-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "askpass.sh")
	script := `#!/bin/sh
case "$1" in
  *sername*|*Username*) printf '%s\n' "$WIKIOS_GIT_ASKPASS_USERNAME" ;;
  *) printf '%s\n' "$WIKIOS_GIT_ASKPASS_TOKEN" ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func configuredSSHCommand(ctx context.Context, repoDir string) string {
	if strings.TrimSpace(repoDir) == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "core.sshCommand")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func NonInteractiveSSHCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "ssh"
	}
	if !strings.Contains(command, "BatchMode") {
		command += " -o BatchMode=yes"
	}
	if !strings.Contains(command, "NumberOfPasswordPrompts") {
		command += " -o NumberOfPasswordPrompts=0"
	}
	return command
}

func RedactRemoteURL(value string) string {
	var out strings.Builder
	tokenStart := -1
	for index, r := range value {
		if unicode.IsSpace(r) {
			if tokenStart >= 0 {
				out.WriteString(redactURLToken(value[tokenStart:index]))
				tokenStart = -1
			}
			out.WriteRune(r)
			continue
		}
		if tokenStart < 0 {
			tokenStart = index
		}
	}
	if tokenStart >= 0 {
		out.WriteString(redactURLToken(value[tokenStart:]))
	}
	return out.String()
}

func DirectoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}

func IsGitRepository(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func isHTTPRemote(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func isSSHRemote(value string) bool {
	if strings.HasPrefix(value, "git@") || strings.Contains(value, "@") && strings.Contains(value, ":") {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "ssh" || strings.HasPrefix(u.Scheme, "git+ssh"))
}

func redactURLToken(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.User == nil {
		return value
	}
	username := u.User.Username()
	if username == "" {
		username = "token"
	}
	u.User = url.UserPassword(username, "redacted")
	return u.String()
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(env[i], prefix))
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
