package service

import "wikios/internal/llm"

func customerRouterResponseFormat() *llm.ResponseFormat {
	return &llm.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &llm.ResponseFormatJSONSchema{
			Name:   "customer_router_output",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"contract_version",
					"specialist",
					"question_stage",
					"user_goal",
					"has_product",
					"needs_product_clarification",
					"clarification_target",
					"answer_strategy",
					"risk_boundary",
					"routing_confidence",
					"routing_reason",
					"intent",
					"rewritten_question",
					"history_summary",
					"slots",
					"ambiguity",
					"missing_info",
					"risk_flags",
					"needs_retrieval",
					"retrieval_queries",
					"handoff_notes",
					"user_intent_signals",
				},
				"properties": map[string]any{
					"contract_version": map[string]any{
						"type": "string",
						"enum": []any{customerRouterContractVersion},
					},
					"specialist": map[string]any{
						"type": "string",
						"enum": []any{"reception", "product", "pricing", "purchase", "technical", "troubleshooting", "billing_after_sales", "safety"},
					},
					"question_stage": map[string]any{
						"type": "string",
						"enum": []any{"goal_consulting", "product_selection", "operation_howto", "troubleshooting", "pricing", "purchase", "after_sales", "safety_boundary", "reception"},
					},
					"user_goal":                   map[string]any{"type": "string"},
					"has_product":                 map[string]any{"type": "boolean"},
					"needs_product_clarification": map[string]any{"type": "boolean"},
					"clarification_target":        map[string]any{"type": "string", "enum": []any{"", "primary_product", "static_type", "ip_type", "bandwidth", "quantity", "scenario", "platform", "device", "error_code", "authentication_method", "account", "order_id", "intent", "none"}},
					"answer_strategy":             map[string]any{"type": "string", "enum": []any{"answer_with_evidence", "recommend_with_boundary", "ask_clarification", "troubleshoot_steps", "quote_or_price", "purchase_guidance", "refuse_with_boundary", "smalltalk"}},
					"risk_boundary":               map[string]any{"type": "string", "enum": []any{"none", "platform_result_not_guaranteed", "safety_refusal", "overseas_access_boundary", "internal_security_boundary", "pricing_review", "after_sales_review"}},
					"routing_confidence":          map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"routing_reason":              map[string]any{"type": "string"},
					"intent":                      map[string]any{"type": "string"},
					"rewritten_question":          map[string]any{"type": "string"},
					"history_summary":             map[string]any{"type": "string"},
					"slots":                       customerRouterSlotsSchema(),
					"ambiguity":                   customerRouterAmbiguitySchema(),
					"missing_info":                enumStringArraySchema([]any{"primary_product", "static_type", "ip_type", "bandwidth", "quantity", "scenario", "platform", "device", "error_code", "authentication_method", "account", "order_id"}, 12),
					"risk_flags":                  enumStringArraySchema([]any{"pricing", "discount", "refund", "billing", "platform_risk", "overseas_access", "compliance", "internal", "illegal", "technical", "troubleshooting", "after_sales", "low_confidence"}, 12),
					"needs_retrieval":             map[string]any{"type": "boolean"},
					"retrieval_queries":           stringArraySchemaWithMax(3),
					"handoff_notes":               map[string]any{"type": "string"},
					"user_intent_signals":         customerRouterUserIntentSignalsSchema(),
				},
			},
		},
	}
}

func customerRouterUserIntentSignalsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"wants_human",
			"wants_wechat",
			"refund_strong",
			"switch_ip",
			"discount_strong",
		},
		"properties": map[string]any{
			"wants_human":     map[string]any{"type": "boolean"},
			"wants_wechat":    map[string]any{"type": "boolean"},
			"refund_strong":   map[string]any{"type": "boolean"},
			"switch_ip":       map[string]any{"type": "boolean"},
			"discount_strong": map[string]any{"type": "boolean"},
		},
	}
}

func customerRouterSlotsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"primary_product",
			"products",
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
			"primary_product": map[string]any{
				"type": "string",
				"enum": []any{"static_ip", "dynamic_ip", "overseas_ip", "residential_ip", "datacenter_ip", "unlimited_ip", "mobile_proxy", "unknown"},
			},
			"products":    enumStringArraySchema([]any{"static_ip", "dynamic_ip", "overseas_ip", "residential_ip", "datacenter_ip", "unlimited_ip", "mobile_proxy", "unknown"}, 8),
			"static_type": map[string]any{"type": "string", "enum": []any{"", "shared", "dedicated", "unknown"}},
			"ip_type":     map[string]any{"type": "string", "enum": []any{"", "datacenter", "residential", "overseas", "mobile", "unknown"}},
			"bandwidth":   map[string]any{"type": "string"},
			"quantity":    map[string]any{"type": "string"},
			"scenario":    map[string]any{"type": "string"},
			"platform":    map[string]any{"type": "string"},
			"device":      map[string]any{"type": "string"},
			"error_code":  map[string]any{"type": "string"},
		},
	}
}

func customerRouterAmbiguitySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"is_ambiguous",
			"ambiguous_fields",
			"reason",
		},
		"properties": map[string]any{
			"is_ambiguous":     map[string]any{"type": "boolean"},
			"ambiguous_fields": enumStringArraySchema([]any{"primary_product", "products", "scenario", "platform", "device", "intent", "target_object"}, 8),
			"reason":           map[string]any{"type": "string"},
		},
	}
}

func customerSpecialistResponseFormat() *llm.ResponseFormat {
	return &llm.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &llm.ResponseFormatJSONSchema{
			Name:   "customer_specialist_answer",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"answer_mode",
					"answer",
					"review_question",
					"confidence_breakdown",
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
					"answer": map[string]any{
						"type":      "string",
						"minLength": 1,
					},
					"review_question": map[string]any{"type": "string"},
					"confidence_breakdown": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required": []any{
							"evidence_coverage",
							"source_directness",
							"answer_specificity",
							"missing_info_impact",
							"risk_sensitivity",
						},
						"properties": map[string]any{
							"evidence_coverage":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							"source_directness":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							"answer_specificity":  map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							"missing_info_impact": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							"risk_sensitivity":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
						},
					},
					"confidence":            map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"evidence_confidence":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
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

func stringArraySchemaWithMax(maxItems int) map[string]any {
	schema := stringArraySchema()
	if maxItems > 0 {
		schema["maxItems"] = maxItems
	}
	return schema
}

func enumStringArraySchema(values []any, maxItems int) map[string]any {
	schema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
			"enum": values,
		},
	}
	if maxItems > 0 {
		schema["maxItems"] = maxItems
	}
	return schema
}
