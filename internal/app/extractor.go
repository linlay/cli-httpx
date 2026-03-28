package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itchyny/gojq"
)

type extractorSpec struct {
	Type    string `toml:"type" json:"type"`
	Expr    string `toml:"expr" json:"expr,omitempty"`
	Pattern string `toml:"pattern" json:"pattern,omitempty"`
	Group   int    `toml:"group" json:"group,omitempty"`
	All     bool   `toml:"all" json:"all,omitempty"`
}

type compiledExtractor struct {
	spec  *extractorSpec
	query *gojq.Query
	regex *regexp.Regexp
}

var extractTemplatePattern = regexp.MustCompile(`\{\{\s*extract\.([A-Za-z0-9_.-]+)\s*\}\}`)

func normalizeExtractor(actionName string, spec *extractorSpec) (*extractorSpec, error) {
	if spec == nil {
		return nil, nil
	}

	normalized := &extractorSpec{
		Type:    strings.ToLower(strings.TrimSpace(spec.Type)),
		Expr:    strings.TrimSpace(spec.Expr),
		Pattern: strings.TrimSpace(spec.Pattern),
		Group:   spec.Group,
		All:     spec.All,
	}

	switch normalized.Type {
	case "jq":
		if normalized.Expr == "" {
			return nil, fmt.Errorf("%w: actions.%s.extract_expr is required when extract_type is jq", ErrConfig, actionName)
		}
		if normalized.Pattern != "" {
			return nil, fmt.Errorf("%w: actions.%s.extract_pattern is only supported when extract_type is regex", ErrConfig, actionName)
		}
		if normalized.Group != 0 {
			return nil, fmt.Errorf("%w: actions.%s.extract_group is only supported when extract_type is regex", ErrConfig, actionName)
		}
		if normalized.All {
			return nil, fmt.Errorf("%w: actions.%s.extract_all is only supported when extract_type is regex", ErrConfig, actionName)
		}
	case "regex":
		if normalized.Pattern == "" {
			return nil, fmt.Errorf("%w: actions.%s.extract_pattern is required when extract_type is regex", ErrConfig, actionName)
		}
		if normalized.Expr != "" {
			return nil, fmt.Errorf("%w: actions.%s.extract_expr is only supported when extract_type is jq", ErrConfig, actionName)
		}
	default:
		return nil, fmt.Errorf("%w: actions.%s.extract_type must be jq or regex", ErrConfig, actionName)
	}

	return normalized, nil
}

func extractorFromAction(actionName string, act action) (*extractorSpec, error) {
	extractType := strings.ToLower(strings.TrimSpace(act.ExtractType))
	extractExpr := strings.TrimSpace(act.ExtractExpr)
	extractPattern := strings.TrimSpace(act.ExtractPattern)
	hasGroup := act.ExtractGroup != nil
	hasAll := act.ExtractAll != nil

	if extractType == "" {
		switch {
		case extractExpr != "":
			return nil, fmt.Errorf("%w: actions.%s.extract_type is required when extract_expr is set", ErrConfig, actionName)
		case extractPattern != "":
			return nil, fmt.Errorf("%w: actions.%s.extract_type is required when extract_pattern is set", ErrConfig, actionName)
		case hasGroup:
			return nil, fmt.Errorf("%w: actions.%s.extract_type is required when extract_group is set", ErrConfig, actionName)
		case hasAll:
			return nil, fmt.Errorf("%w: actions.%s.extract_type is required when extract_all is set", ErrConfig, actionName)
		default:
			return nil, nil
		}
	}

	spec := &extractorSpec{
		Type:    extractType,
		Expr:    extractExpr,
		Pattern: extractPattern,
	}
	if hasGroup {
		spec.Group = *act.ExtractGroup
	}
	if hasAll {
		spec.All = *act.ExtractAll
	}

	return normalizeExtractor(actionName, spec)
}

func compileExtractor(actionName string, spec *extractorSpec, _ map[string]any) (*compiledExtractor, error) {
	if spec == nil {
		return nil, nil
	}

	switch spec.Type {
	case "jq":
		query, err := parseJQ(spec.Expr)
		if err != nil {
			return nil, fmt.Errorf("%w: actions.%s.extract_expr: %v", ErrConfig, actionName, err)
		}
		return &compiledExtractor{
			spec:  cloneExtractorSpec(spec),
			query: query,
		}, nil
	case "regex":
		if strings.Contains(spec.Pattern, "{{") {
			return &compiledExtractor{
				spec: cloneExtractorSpec(spec),
			}, nil
		}
		re, err := regexp.Compile(spec.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: actions.%s.extract_pattern: %v", ErrConfig, actionName, err)
		}
		if spec.Group < 0 || spec.Group > re.NumSubexp() {
			return nil, fmt.Errorf("%w: actions.%s.extract_group %d is out of range for pattern with %d groups", ErrConfig, actionName, spec.Group, re.NumSubexp())
		}
		return &compiledExtractor{
			spec:  cloneExtractorSpec(spec),
			regex: re,
		}, nil
	default:
		return nil, fmt.Errorf("%w: actions.%s.extract_type must be jq or regex", ErrConfig, actionName)
	}
}

func resolveExtractTemplate(pattern string, extractInput map[string]any) (string, error) {
	if !strings.Contains(pattern, "{{") {
		return pattern, nil
	}

	matches := extractTemplatePattern.FindAllStringSubmatchIndex(pattern, -1)
	if len(matches) == 0 {
		return pattern, nil
	}

	var out strings.Builder
	last := 0
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		start, end := match[0], match[1]
		keyStart, keyEnd := match[2], match[3]
		out.WriteString(pattern[last:start])

		value, err := lookupExtractValue(extractInput, pattern[keyStart:keyEnd])
		if err != nil {
			return "", err
		}
		rendered, err := stringifyExtractTemplateValue(value)
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
		last = end
	}
	out.WriteString(pattern[last:])
	return out.String(), nil
}

func lookupExtractValue(input map[string]any, path string) (any, error) {
	if input == nil {
		return nil, fmt.Errorf("extract input %q not provided", path)
	}
	current := any(input)
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("extract input %q not provided", path)
		}
		value, ok := object[part]
		if !ok {
			return nil, fmt.Errorf("extract input %q not provided", path)
		}
		current = value
	}
	return current, nil
}

func stringifyExtractTemplateValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", fmt.Errorf("extract template value must be a scalar, got null")
	case string:
		return typed, nil
	case bool, int, int8, int16, int32, int64, float32, float64:
		return fmt.Sprint(typed), nil
	default:
		return "", fmt.Errorf("extract template value must be a scalar, got %T", value)
	}
}

func cloneExtractorSpec(spec *extractorSpec) *extractorSpec {
	if spec == nil {
		return nil
	}
	cp := *spec
	return &cp
}

func executeExtractor(extractor *compiledExtractor, input any, rawBody []byte) (any, error) {
	if extractor == nil {
		return nil, nil
	}

	switch extractor.spec.Type {
	case "jq":
		return runParsedJQ(extractor.query, input, false)
	case "regex":
		return runRegexExtractor(extractor, input, string(rawBody))
	default:
		return nil, fmt.Errorf("unsupported extractor type %q", extractor.spec.Type)
	}
}

func runRegexExtractor(extractor *compiledExtractor, input any, text string) (any, error) {
	re := extractor.regex
	if re == nil {
		pattern, err := resolveExtractTemplate(extractor.spec.Pattern, extractInputFromContext(input))
		if err != nil {
			return nil, err
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile regex extractor pattern: %v", err)
		}
		if extractor.spec.Group < 0 || extractor.spec.Group > re.NumSubexp() {
			return nil, fmt.Errorf("regex extractor group %d is out of range", extractor.spec.Group)
		}
	}

	group := extractor.spec.Group
	if extractor.spec.All {
		matches := re.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return nil, nil
		}
		values := make([]string, 0, len(matches))
		for _, match := range matches {
			if group >= len(match) {
				return nil, fmt.Errorf("regex extractor group %d is out of range", group)
			}
			values = append(values, match[group])
		}
		return values, nil
	}

	match := re.FindStringSubmatch(text)
	if len(match) == 0 {
		return nil, nil
	}
	if group >= len(match) {
		return nil, fmt.Errorf("regex extractor group %d is out of range", group)
	}
	return match[group], nil
}

func extractInputFromContext(input any) map[string]any {
	ctx, ok := input.(map[string]any)
	if !ok {
		return nil
	}
	value, ok := ctx["extract"]
	if !ok {
		return nil
	}
	object, _ := value.(map[string]any)
	return object
}

func renderExtractOutput(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case string:
		return []byte(typed), nil
	case bool, int, int8, int16, int32, int64, float32, float64:
		return []byte(fmt.Sprint(typed)), nil
	default:
		content, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return content, nil
	}
}
