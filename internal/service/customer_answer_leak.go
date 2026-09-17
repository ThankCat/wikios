package service

import "strings"

func customerVisibleAnswerLeaksInternalContext(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"技术专家",
		"产品专家",
		"价格专家",
		"售后专家",
		"安全专家",
		"前台接待",
		"转接专家",
		"转给专家",
		"安排专家",
		"分派给专家",
		"router",
		"specialist",
		"candidate_pages",
		"candidate page",
		"candidate_page_paths",
		"source_pages",
		"wiki/",
		"raw/",
		".md",
		".xlsx",
		"/users/chenhao/",
		"review_question",
		"answer_mode",
		"evidence_confidence",
		"review_required",
		"系统提示词",
		"内部提示词",
		"系统检索",
		"系统未收录",
		"系统里没有",
		"资料库",
		"知识库",
		"prompt",
		"json 字段",
		"json字段",
		"产品价格档位表",
		"四叶天产品定价修订版",
		"客服最低授权价",
		"最低授权价",
		"审批阈值",
		"采购成本",
		"毛利",
	} {
		if strings.Contains(normalized, strings.ToLower(marker)) {
			return true
		}
	}
	for _, phrase := range []string{
		"知识库没有",
		"知识库暂无",
		"知识库显示",
		"知识库提示",
		"资料库中暂无",
		"资料库暂无",
		"资料里没有",
		"资料暂无",
		"查询资料",
		"查阅资料",
		"检索资料",
		"根据资料",
		"资料提示",
		"资料显示",
		"候选资料",
		"候选知识",
		"候选页面",
		"候选页",
		"来源文件",
		"来源路径",
		"工作表显示",
		"根据价格表",
		"按价格表",
		"根据表格",
		"按表格",
		"检索结果",
		"路由判断",
		"分诊结果",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
