package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"wikios/internal/runtime"
)

type RetrievedPage struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

type QMDRetriever struct {
	rt *runtime.Runtime
}

func NewQMDRetriever(rt *runtime.Runtime) *QMDRetriever {
	return &QMDRetriever{rt: rt}
}

func (r *QMDRetriever) Retrieve(ctx context.Context, env *runtime.ExecEnv, question string, topK int) ([]RetrievedPage, error) {
	result, err := r.rt.Execute(ctx, env, runtime.ToolCall{
		Name: "exec.qmd",
		Args: map[string]any{
			"subcommand": "query",
			"question":   question,
		},
	})
	if err == nil && result.Success {
		out := parseQMDQuery(result.Data["stdout"])
		if len(out) > 0 {
			sort.Slice(out, func(i, j int) bool {
				return out[i].Score > out[j].Score
			})
			if topK > 0 && len(out) > topK {
				out = out[:topK]
			}
			return out, nil
		}
	}
	var out []RetrievedPage
	fallback, fallbackErr := r.rt.Execute(ctx, env, runtime.ToolCall{
		Name: "wiki.search_pages",
		Args: map[string]any{"query": question},
	})
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	if !fallback.Success {
		if result.Error != nil {
			return nil, errors.New(result.Error.Message)
		}
		return nil, errors.New(fallback.Error.Message)
	}
	if raw, ok := fallback.Data["matches"].([]map[string]any); ok {
		for _, item := range raw {
			path, _ := item["path"].(string)
			score, _ := item["score"].(int)
			out = append(out, RetrievedPage{Path: path, Score: float64(score)})
		}
	}
	if len(out) == 0 {
		out = append(out, RetrievedPage{Path: "wiki/index.md", Score: 1})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func parseQMDQuery(stdout any) []RetrievedPage {
	raw, ok := stdout.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var generic []any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil
	}
	out := make([]RetrievedPage, 0, len(generic))
	for _, item := range generic {
		switch typed := item.(type) {
		case string:
			path := normalizeRetrievedPath(typed)
			if path == "" {
				continue
			}
			out = append(out, RetrievedPage{Path: path, Score: 1})
		case map[string]any:
			path := ""
			for _, key := range []string{"path", "file", "document", "source"} {
				if s, ok := typed[key].(string); ok && s != "" {
					path = normalizeRetrievedPath(s)
					break
				}
			}
			if path == "" {
				continue
			}
			score := 1.0
			if v, ok := typed["score"].(float64); ok {
				score = v
			}
			out = append(out, RetrievedPage{Path: path, Score: score})
		}
	}
	return out
}

func normalizeRetrievedPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "qmd://") {
		path = strings.TrimPrefix(path, "qmd://")
		path = strings.TrimLeft(path, "/")
	}
	return path
}
