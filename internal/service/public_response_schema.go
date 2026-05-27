package service

import "wikios/internal/llm"

func publicRouterResponseFormat() *llm.ResponseFormat {
	return &llm.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &llm.ResponseFormatJSONSchema{
			Name:   "public_router_output",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"specialist",
					"intent",
					"rewritten_question",
					"history_summary",
					"slots",
					"missing_info",
					"risk_flags",
					"needs_retrieval",
					"retrieval_queries",
					"answer_policy",
				},
				"properties": map[string]any{
					"specialist": map[string]any{
						"type": "string",
						"enum": []any{"reception", "product", "pricing", "purchase", "technical", "troubleshooting", "billing_after_sales", "safety"},
					},
					"intent":             map[string]any{"type": "string"},
					"rewritten_question": map[string]any{"type": "string"},
					"history_summary":    map[string]any{"type": "string"},
					"slots":              publicRouterSlotsSchema(),
					"missing_info":       stringArraySchema(),
					"risk_flags":         stringArraySchema(),
					"needs_retrieval":    map[string]any{"type": "boolean"},
					"retrieval_queries":  stringArraySchema(),
					"answer_policy":      map[string]any{"type": "string"},
				},
			},
		},
	}
}

func publicRouterSlotsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"product",
			"products",
			"product_resolution",
			"static_type",
			"ip_type",
			"bandwidth",
			"quantity",
			"scenario",
			"platform",
			"device",
			"error_code",
		},
		"properties": map[string]any{
			"product":            map[string]any{"type": "string"},
			"products":           stringArraySchema(),
			"product_resolution": publicRouterProductResolutionSchema(),
			"static_type":        map[string]any{"type": "string"},
			"ip_type":            map[string]any{"type": "string"},
			"bandwidth":          map[string]any{"type": "string"},
			"quantity":           map[string]any{"type": "string"},
			"scenario":           map[string]any{"type": "string"},
			"platform":           map[string]any{"type": "string"},
			"device":             map[string]any{"type": "string"},
			"error_code":         map[string]any{"type": "string"},
		},
	}
}

func publicRouterProductResolutionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"primary",
			"all",
			"from_history",
			"confidence",
			"ambiguous",
			"reason",
		},
		"properties": map[string]any{
			"primary":      map[string]any{"type": "string"},
			"all":          stringArraySchema(),
			"from_history": map[string]any{"type": "boolean"},
			"confidence":   map[string]any{"type": "number"},
			"ambiguous":    map[string]any{"type": "boolean"},
			"reason":       map[string]any{"type": "string"},
		},
	}
}

func publicSpecialistResponseFormat() *llm.ResponseFormat {
	return &llm.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &llm.ResponseFormatJSONSchema{
			Name:   "public_specialist_answer",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"answer_mode",
					"answer",
					"review_question",
					"confidence",
					"evidence_confidence",
					"review_required",
					"review_reason",
					"suggested_target_path",
					"sources",
					"notes",
				},
				"properties": map[string]any{
					"answer_mode": map[string]any{
						"type": "string",
						"enum": []any{"evidence", "mixed", "self_answer", "clarification", "refusal"},
					},
					"answer":                map[string]any{"type": "string"},
					"review_question":       map[string]any{"type": "string"},
					"confidence":            map[string]any{"type": "number"},
					"evidence_confidence":   map[string]any{"type": "number"},
					"review_required":       map[string]any{"type": "boolean"},
					"review_reason":         map[string]any{"type": "string"},
					"suggested_target_path": map[string]any{"type": "string"},
					"sources": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []any{"path", "confidence"},
							"properties": map[string]any{
								"path": map[string]any{"type": "string"},
								"confidence": map[string]any{
									"type": "string",
									"enum": []any{"low", "medium", "high"},
								},
							},
						},
					},
					"notes": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
}
