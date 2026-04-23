package runtime

import "fmt"

type PolicyEngine struct{}

func NewPolicyEngine() *PolicyEngine {
	return &PolicyEngine{}
}

func (p *PolicyEngine) Allow(env *ExecEnv, tool Tool, _ map[string]any) error {
	if env == nil {
		return fmt.Errorf("exec env is required")
	}
	switch env.Mode {
	case "public":
		if !publicAllowed(tool.Name()) {
			return fmt.Errorf("tool %s is not allowed in public mode", tool.Name())
		}
	case "admin":
		if !adminAllowed(tool.Name()) {
			return fmt.Errorf("tool %s is not allowed in admin mode", tool.Name())
		}
	case "admin_direct":
		if !adminDirectAllowed(tool.Name()) {
			return fmt.Errorf("tool %s is not allowed in admin_direct mode", tool.Name())
		}
	default:
		return fmt.Errorf("unsupported mode %q", env.Mode)
	}
	return nil
}

func adminDirectAllowed(name string) bool {
	switch name {
	case "exec.shell":
		return true
	default:
		return false
	}
}

func publicAllowed(name string) bool {
	switch name {
	case "fs.read_file", "fs.list_dir", "fs.file_stat", "fs.glob",
		"wiki.read_page", "wiki.search_pages", "wiki.find_by_slug", "wiki.find_by_alias",
		"exec.qmd":
		return true
	default:
		return false
	}
}

func adminAllowed(name string) bool {
	if publicAllowed(name) {
		return true
	}
	switch name {
	case "hash.sha256",
		"wiki.create_from_template", "wiki.patch_page", "wiki.append_log", "wiki.write_output",
		"wiki.update_index_entry", "wiki.update_questions",
		"workspace.create_job_dir", "workspace.write_temp_file", "workspace.read_temp_file",
		"workspace.commit_temp_to_wiki", "workspace.discard",
		"exec.python", "lint.run",
		"repair.apply_low_risk", "repair.create_high_risk_proposal",
		"git.status", "git.commit", "git.push":
		return true
	default:
		return false
	}
}
