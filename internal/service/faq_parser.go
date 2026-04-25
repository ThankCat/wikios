package service

import "strings"

type structuredFAQParseResult struct {
	Content   string
	Dataset   *canonicalFAQDataset
	StoredExt string
}

type structuredFAQParser interface {
	Parse(filename string, titleHint string, ext string, raw []byte, text string, profile *knowledgeProfile) (*structuredFAQParseResult, bool, error)
}

var structuredFAQParsers = []structuredFAQParser{
	faqXLSXUploadParser{},
	faqTextUploadParser{},
}

func parseStructuredFAQUpload(filename string, titleHint string, ext string, raw []byte, text string) (*structuredFAQParseResult, error) {
	return parseStructuredFAQUploadWithProfile(filename, titleHint, ext, raw, text, nil)
}

func parseStructuredFAQUploadWithProfile(filename string, titleHint string, ext string, raw []byte, text string, profile *knowledgeProfile) (*structuredFAQParseResult, error) {
	for _, parser := range structuredFAQParsers {
		result, matched, err := parser.Parse(filename, titleHint, ext, raw, text, profile)
		if err != nil || matched {
			return result, err
		}
	}
	return nil, nil
}

type faqXLSXUploadParser struct{}

func (faqXLSXUploadParser) Parse(filename string, titleHint string, ext string, raw []byte, _ string, profile *knowledgeProfile) (*structuredFAQParseResult, bool, error) {
	if strings.ToLower(ext) != ".xlsx" {
		return nil, false, nil
	}
	content, dataset, err := parseFAQXLSXDatasetWithProfile(filename, titleHint, raw, profile)
	if err != nil {
		return nil, true, err
	}
	return &structuredFAQParseResult{
		Content:   content,
		Dataset:   dataset,
		StoredExt: ".json",
	}, true, nil
}

type faqTextUploadParser struct{}

func (faqTextUploadParser) Parse(filename string, titleHint string, ext string, _ []byte, text string, profile *knowledgeProfile) (*structuredFAQParseResult, bool, error) {
	if strings.ToLower(ext) == ".xlsx" || strings.TrimSpace(text) == "" {
		return nil, false, nil
	}
	dataset, err := detectCanonicalFAQDatasetWithProfile(filename, titleHint, text, profile)
	if err != nil {
		return nil, true, err
	}
	if dataset == nil {
		return nil, false, nil
	}
	return &structuredFAQParseResult{
		Content:   text,
		Dataset:   dataset,
		StoredExt: ext,
	}, true, nil
}
