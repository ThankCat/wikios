package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"wikios/internal/config"
	"wikios/internal/store"
)

const RuntimeSettingsKey = "runtime_settings"
const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

type RuntimeSettings struct {
	PublicQuery RuntimePublicQuerySettings `json:"public_query"`
	Support     RuntimeSupportSettings     `json:"support"`
	AnswerLog   RuntimeAnswerLogSettings   `json:"answer_log"`
	Knowledge   RuntimeKnowledgeSettings   `json:"knowledge"`
	Sync        RuntimeSyncSettings        `json:"sync"`
}

type RuntimePublicQuerySettings struct {
	DirectMin                float64 `json:"direct_min"`
	ReviewMin                float64 `json:"review_min"`
	CandidateTopK            int     `json:"candidate_top_k"`
	MaxEvidenceChars         int     `json:"max_evidence_chars"`
	RouterModelID            string  `json:"router_model_id,omitempty"`
	SpecialistModelID        string  `json:"specialist_model_id,omitempty"`
	RouterEnableThinking     *bool   `json:"router_enable_thinking,omitempty"`
	SpecialistEnableThinking *bool   `json:"specialist_enable_thinking,omitempty"`
}

type RuntimeSupportSettings struct {
	Phone string `json:"phone"`
	WeCom string `json:"wecom"`
}

type RuntimeAnswerLogSettings struct {
	Enabled       bool `json:"enabled"`
	Redact        bool `json:"redact"`
	RetentionDays int  `json:"retention_days"`
}

type RuntimeKnowledgeSettings struct {
	MaxTextFileKB int `json:"max_text_file_kb"`
}

type RuntimeSyncSettings struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

type RuntimeEnvironmentSettings struct {
	ServerPort        int    `json:"server_port"`
	ServerMode        string `json:"server_mode"`
	WikiRoot          string `json:"wiki_root"`
	WikiName          string `json:"wiki_name"`
	QMDIndex          string `json:"qmd_index"`
	WorkspaceDir      string `json:"workspace_dir"`
	SQLitePath        string `json:"sqlite_path"`
	WebDistDir        string `json:"web_dist_dir"`
	WebEnabled        bool   `json:"web_enabled"`
	PublicIntentsPath string `json:"public_intents_path"`
}

type RuntimeSettingsSnapshot struct {
	Settings    RuntimeSettings            `json:"settings"`
	Defaults    RuntimeSettings            `json:"defaults"`
	UpdatedAt   string                     `json:"updated_at,omitempty"`
	Environment RuntimeEnvironmentSettings `json:"environment"`
}

func DefaultRuntimeSettings(cfg *config.Config) RuntimeSettings {
	routerThinking := false
	specialistThinking := true
	settings := RuntimeSettings{
		PublicQuery: RuntimePublicQuerySettings{
			DirectMin:                0.70,
			ReviewMin:                0.25,
			CandidateTopK:            6,
			MaxEvidenceChars:         2400,
			RouterEnableThinking:     &routerThinking,
			SpecialistEnableThinking: &specialistThinking,
		},
		Support: RuntimeSupportSettings{
			Phone: "400-1080-106",
			WeCom: "企业微信",
		},
		AnswerLog: RuntimeAnswerLogSettings{
			Enabled:       true,
			Redact:        true,
			RetentionDays: 14,
		},
		Knowledge: RuntimeKnowledgeSettings{
			MaxTextFileKB: 500,
		},
		Sync: RuntimeSyncSettings{
			Remote: "origin",
			Branch: "main",
		},
	}
	if cfg == nil {
		return settings
	}
	if cfg.PublicQuery.Confidence.DirectMin > 0 {
		settings.PublicQuery.DirectMin = cfg.PublicQuery.Confidence.DirectMin
	}
	if cfg.PublicQuery.Confidence.ReviewMin > 0 {
		settings.PublicQuery.ReviewMin = cfg.PublicQuery.Confidence.ReviewMin
	}
	if cfg.PublicQuery.CandidateTopK > 0 {
		settings.PublicQuery.CandidateTopK = cfg.PublicQuery.CandidateTopK
	}
	if cfg.PublicQuery.MaxEvidenceChars > 0 {
		settings.PublicQuery.MaxEvidenceChars = cfg.PublicQuery.MaxEvidenceChars
	}
	if strings.TrimSpace(cfg.Support.Phone) != "" {
		settings.Support.Phone = strings.TrimSpace(cfg.Support.Phone)
	}
	if strings.TrimSpace(cfg.Support.WeCom) != "" {
		settings.Support.WeCom = strings.TrimSpace(cfg.Support.WeCom)
	}
	if cfg.PublicQuery.AnswerLog.Enabled != nil {
		settings.AnswerLog.Enabled = *cfg.PublicQuery.AnswerLog.Enabled
	}
	if cfg.PublicQuery.AnswerLog.Redact != nil {
		settings.AnswerLog.Redact = *cfg.PublicQuery.AnswerLog.Redact
	}
	if cfg.PublicQuery.AnswerLog.RetentionDays > 0 {
		settings.AnswerLog.RetentionDays = cfg.PublicQuery.AnswerLog.RetentionDays
	}
	if cfg.Upload.MaxTextFileKB > 0 {
		settings.Knowledge.MaxTextFileKB = cfg.Upload.MaxTextFileKB
	}
	if strings.TrimSpace(cfg.Sync.Remote) != "" {
		settings.Sync.Remote = strings.TrimSpace(cfg.Sync.Remote)
	}
	if strings.TrimSpace(cfg.Sync.Branch) != "" {
		settings.Sync.Branch = strings.TrimSpace(cfg.Sync.Branch)
	}
	return NormalizeRuntimeSettings(settings, DefaultRuntimeSettings(nil))
}

func RuntimeEnvironmentFromConfig(cfg *config.Config) RuntimeEnvironmentSettings {
	if cfg == nil {
		return RuntimeEnvironmentSettings{}
	}
	webEnabled := true
	if cfg.Web.Enabled != nil {
		webEnabled = *cfg.Web.Enabled
	}
	return RuntimeEnvironmentSettings{
		ServerPort:        cfg.Server.Port,
		ServerMode:        cfg.Server.Mode,
		WikiRoot:          cfg.MountedWiki.Root,
		WikiName:          cfg.MountedWiki.Name,
		QMDIndex:          cfg.MountedWiki.QMDIndex,
		WorkspaceDir:      cfg.Workspace.BaseDir,
		SQLitePath:        cfg.Storage.SQLitePath,
		WebDistDir:        cfg.Web.DistDir,
		WebEnabled:        webEnabled,
		PublicIntentsPath: cfg.PublicIntents.Path,
	}
}

func LoadRuntimeSettings(ctx context.Context, dataStore *store.Store, cfg *config.Config) (RuntimeSettingsSnapshot, error) {
	defaults := DefaultRuntimeSettings(cfg)
	snapshot := RuntimeSettingsSnapshot{
		Settings:    defaults,
		Defaults:    defaults,
		Environment: RuntimeEnvironmentFromConfig(cfg),
	}
	if dataStore == nil {
		return snapshot, nil
	}
	setting, err := dataStore.GetAdminSetting(ctx, RuntimeSettingsKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return snapshot, nil
		}
		return snapshot, err
	}
	var parsed RuntimeSettings
	if err := json.Unmarshal([]byte(setting.ValueJSON), &parsed); err != nil {
		return snapshot, fmt.Errorf("decode runtime settings: %w", err)
	}
	merged := MergeRuntimeSettings(defaults, parsed)
	if fields := ValidateRuntimeSettings(merged); len(fields) > 0 {
		return snapshot, fmt.Errorf("stored runtime settings are invalid")
	}
	snapshot.Settings = merged
	if !setting.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = setting.UpdatedAt.Format(timeRFC3339Nano)
	}
	return snapshot, nil
}

func SaveRuntimeSettings(ctx context.Context, dataStore *store.Store, cfg *config.Config, settings RuntimeSettings) (RuntimeSettingsSnapshot, map[string]string, error) {
	defaults := DefaultRuntimeSettings(cfg)
	normalized := TrimRuntimeSettings(settings)
	if fields := ValidateRuntimeSettings(normalized); len(fields) > 0 {
		return RuntimeSettingsSnapshot{
			Settings:    normalized,
			Defaults:    defaults,
			Environment: RuntimeEnvironmentFromConfig(cfg),
		}, fields, nil
	}
	if dataStore == nil {
		return RuntimeSettingsSnapshot{
			Settings:    normalized,
			Defaults:    defaults,
			Environment: RuntimeEnvironmentFromConfig(cfg),
		}, nil, fmt.Errorf("store is unavailable")
	}
	setting, err := dataStore.SetAdminSetting(ctx, RuntimeSettingsKey, normalized)
	if err != nil {
		return RuntimeSettingsSnapshot{
			Settings:    normalized,
			Defaults:    defaults,
			Environment: RuntimeEnvironmentFromConfig(cfg),
		}, nil, err
	}
	return RuntimeSettingsSnapshot{
		Settings:    normalized,
		Defaults:    defaults,
		UpdatedAt:   setting.UpdatedAt.Format(timeRFC3339Nano),
		Environment: RuntimeEnvironmentFromConfig(cfg),
	}, nil, nil
}

func MergeRuntimeSettings(defaults RuntimeSettings, override RuntimeSettings) RuntimeSettings {
	settings := defaults
	if override.PublicQuery.DirectMin != 0 {
		settings.PublicQuery.DirectMin = override.PublicQuery.DirectMin
	}
	if override.PublicQuery.ReviewMin != 0 {
		settings.PublicQuery.ReviewMin = override.PublicQuery.ReviewMin
	}
	if override.PublicQuery.CandidateTopK != 0 {
		settings.PublicQuery.CandidateTopK = override.PublicQuery.CandidateTopK
	}
	if override.PublicQuery.MaxEvidenceChars != 0 {
		settings.PublicQuery.MaxEvidenceChars = override.PublicQuery.MaxEvidenceChars
	}
	if strings.TrimSpace(override.PublicQuery.RouterModelID) != "" {
		settings.PublicQuery.RouterModelID = override.PublicQuery.RouterModelID
	}
	if strings.TrimSpace(override.PublicQuery.SpecialistModelID) != "" {
		settings.PublicQuery.SpecialistModelID = override.PublicQuery.SpecialistModelID
	}
	if override.PublicQuery.RouterEnableThinking != nil {
		value := *override.PublicQuery.RouterEnableThinking
		settings.PublicQuery.RouterEnableThinking = &value
	}
	if override.PublicQuery.SpecialistEnableThinking != nil {
		value := *override.PublicQuery.SpecialistEnableThinking
		settings.PublicQuery.SpecialistEnableThinking = &value
	}
	if strings.TrimSpace(override.Support.Phone) != "" {
		settings.Support.Phone = override.Support.Phone
	}
	if strings.TrimSpace(override.Support.WeCom) != "" {
		settings.Support.WeCom = override.Support.WeCom
	}
	settings.AnswerLog = override.AnswerLog
	if override.AnswerLog.RetentionDays == 0 {
		settings.AnswerLog = defaults.AnswerLog
	}
	if override.Knowledge.MaxTextFileKB != 0 {
		settings.Knowledge.MaxTextFileKB = override.Knowledge.MaxTextFileKB
	}
	if strings.TrimSpace(override.Sync.Remote) != "" {
		settings.Sync.Remote = override.Sync.Remote
	}
	if strings.TrimSpace(override.Sync.Branch) != "" {
		settings.Sync.Branch = override.Sync.Branch
	}
	return NormalizeRuntimeSettings(settings, defaults)
}

func NormalizeRuntimeSettings(settings RuntimeSettings, defaults RuntimeSettings) RuntimeSettings {
	if settings.PublicQuery.DirectMin == 0 {
		settings.PublicQuery.DirectMin = defaults.PublicQuery.DirectMin
	}
	if settings.PublicQuery.ReviewMin == 0 {
		settings.PublicQuery.ReviewMin = defaults.PublicQuery.ReviewMin
	}
	if settings.PublicQuery.CandidateTopK == 0 {
		settings.PublicQuery.CandidateTopK = defaults.PublicQuery.CandidateTopK
	}
	if settings.PublicQuery.MaxEvidenceChars == 0 {
		settings.PublicQuery.MaxEvidenceChars = defaults.PublicQuery.MaxEvidenceChars
	}
	settings.PublicQuery.RouterModelID = strings.TrimSpace(settings.PublicQuery.RouterModelID)
	settings.PublicQuery.SpecialistModelID = strings.TrimSpace(settings.PublicQuery.SpecialistModelID)
	settings.PublicQuery.RouterEnableThinking = cloneBoolPtr(settings.PublicQuery.RouterEnableThinking)
	settings.PublicQuery.SpecialistEnableThinking = cloneBoolPtr(settings.PublicQuery.SpecialistEnableThinking)
	if strings.TrimSpace(settings.Support.Phone) == "" {
		settings.Support.Phone = defaults.Support.Phone
	} else {
		settings.Support.Phone = strings.TrimSpace(settings.Support.Phone)
	}
	if strings.TrimSpace(settings.Support.WeCom) == "" {
		settings.Support.WeCom = defaults.Support.WeCom
	} else {
		settings.Support.WeCom = strings.TrimSpace(settings.Support.WeCom)
	}
	if settings.AnswerLog.RetentionDays == 0 {
		settings.AnswerLog = defaults.AnswerLog
	}
	if settings.Knowledge.MaxTextFileKB == 0 {
		settings.Knowledge.MaxTextFileKB = defaults.Knowledge.MaxTextFileKB
	}
	if strings.TrimSpace(settings.Sync.Remote) == "" {
		settings.Sync.Remote = defaults.Sync.Remote
	} else {
		settings.Sync.Remote = strings.TrimSpace(settings.Sync.Remote)
	}
	if strings.TrimSpace(settings.Sync.Branch) == "" {
		settings.Sync.Branch = defaults.Sync.Branch
	} else {
		settings.Sync.Branch = strings.TrimSpace(settings.Sync.Branch)
	}
	return settings
}

func TrimRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	settings.PublicQuery.RouterModelID = strings.TrimSpace(settings.PublicQuery.RouterModelID)
	settings.PublicQuery.SpecialistModelID = strings.TrimSpace(settings.PublicQuery.SpecialistModelID)
	settings.PublicQuery.RouterEnableThinking = cloneBoolPtr(settings.PublicQuery.RouterEnableThinking)
	settings.PublicQuery.SpecialistEnableThinking = cloneBoolPtr(settings.PublicQuery.SpecialistEnableThinking)
	settings.Support.Phone = strings.TrimSpace(settings.Support.Phone)
	settings.Support.WeCom = strings.TrimSpace(settings.Support.WeCom)
	settings.Sync.Remote = strings.TrimSpace(settings.Sync.Remote)
	settings.Sync.Branch = strings.TrimSpace(settings.Sync.Branch)
	return settings
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func ValidateRuntimeSettings(settings RuntimeSettings) map[string]string {
	fields := map[string]string{}
	if settings.PublicQuery.DirectMin < 0 || settings.PublicQuery.DirectMin > 1 {
		fields["public_query.direct_min"] = "must be between 0 and 1"
	}
	if settings.PublicQuery.ReviewMin < 0 || settings.PublicQuery.ReviewMin > 1 {
		fields["public_query.review_min"] = "must be between 0 and 1"
	}
	if settings.PublicQuery.ReviewMin > settings.PublicQuery.DirectMin {
		fields["public_query.review_min"] = "must be less than or equal to public_query.direct_min"
	}
	if settings.PublicQuery.CandidateTopK < 1 || settings.PublicQuery.CandidateTopK > 20 {
		fields["public_query.candidate_top_k"] = "must be between 1 and 20"
	}
	if settings.PublicQuery.MaxEvidenceChars < 200 || settings.PublicQuery.MaxEvidenceChars > 20000 {
		fields["public_query.max_evidence_chars"] = "must be between 200 and 20000"
	}
	if settings.AnswerLog.RetentionDays < 1 || settings.AnswerLog.RetentionDays > 365 {
		fields["answer_log.retention_days"] = "must be between 1 and 365"
	}
	if settings.Knowledge.MaxTextFileKB < 1 {
		fields["knowledge.max_text_file_kb"] = "must be a positive integer"
	}
	if strings.TrimSpace(settings.Sync.Remote) == "" {
		fields["sync.remote"] = "must not be empty"
	}
	if strings.TrimSpace(settings.Sync.Branch) == "" {
		fields["sync.branch"] = "must not be empty"
	}
	return fields
}

func LoadRuntimeSettingsOrDefault(ctx context.Context, dataStore *store.Store, cfg *config.Config) RuntimeSettings {
	snapshot, err := LoadRuntimeSettings(ctx, dataStore, cfg)
	if err != nil {
		return DefaultRuntimeSettings(cfg)
	}
	return snapshot.Settings
}
