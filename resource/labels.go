package resource

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type LabelSelectorOperator string

const LabelSelectorOperatorIn LabelSelectorOperator = "In"

var allowedOperators = labelSelectorOperators{LabelSelectorOperatorIn}

type labelSelectorOperators []LabelSelectorOperator

func (l labelSelectorOperators) String() string {
	var mapped []string

	for _, op := range l {
		mapped = append(mapped, string(op))
	}

	return strings.Join(mapped, ",")
}

const maxLabelPartRunes = 255

// ValidateLabels validates resource labels:
// keys must be non-empty and neither keys nor values may contain "=".
func ValidateLabels(labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}

	for _, key := range keys {
		if err := validateLabelPartKey(key); err != nil {
			return fmt.Errorf("invalid label key %q: %w", key, err)
		}
		if err := validateLabelPartValue(labels[key]); err != nil {
			return fmt.Errorf("invalid label value for %q: %w", key, err)
		}
	}

	return nil
}

type LabelSelector struct {
	Key      string
	Operator LabelSelectorOperator
	Values   []string
}

// ValidateLabelSelectors validates IN-only label selector requirements.
func ValidateLabelSelectors(labelSelectors []LabelSelector) error {
	for i, labelSelector := range labelSelectors {
		if labelSelector.Key == "" {
			return fmt.Errorf("invalid label selector requirement %d: key is required", i)
		}

		if err := validateLabelPartKey(labelSelector.Key); err != nil {
			return fmt.Errorf("invalid label selector requirement %d key %q: %w", i, labelSelector.Key, err)
		}

		if !slices.Contains(allowedOperators, labelSelector.Operator) {
			return fmt.Errorf(
				"invalid label selector requirement %d operator %q: only [%s] is supported",
				i,
				labelSelector.Operator,
				allowedOperators.String(),
			)
		}

		for valueIndex, value := range labelSelector.Values {
			if err := validateLabelPartValue(value); err != nil {
				return fmt.Errorf(
					"invalid label selector requirement %d value %d %q: %w",
					i,
					valueIndex,
					value,
					err,
				)
			}
		}
	}

	return nil
}

// FormatLabelSelectors converts IN-only requirements into a selector string.
func FormatLabelSelectors(labelSelectors []LabelSelector) (string, error) {
	if len(labelSelectors) == 0 {
		return "", nil
	}
	if err := ValidateLabelSelectors(labelSelectors); err != nil {
		return "", err
	}

	formattedSelectors := make([]string, 0, len(labelSelectors))
	for _, labelSelector := range labelSelectors {
		values := normalizeEmptyLabelSelectorValues(labelSelector.Values)
		formattedValues := make([]string, 0, len(values))
		for _, value := range values {
			formattedValues = append(formattedValues, formatLabelSelectorValue(value))
		}

		formattedSelectors = append(
			formattedSelectors,
			fmt.Sprintf("%s in (%s)", labelSelector.Key, strings.Join(formattedValues, ",")),
		)
	}

	return strings.Join(formattedSelectors, ","), nil
}

// ParseLabelSelectors parses a selector string into IN-only requirements.
func ParseLabelSelectors(selector string) ([]LabelSelector, error) {
	if selector == "" {
		return nil, nil
	}

	selectorParts, err := splitLabelSelectorParts(selector)
	if err != nil {
		return nil, err
	}

	labelSelectors := make([]LabelSelector, 0, len(selectorParts))
	for i, selectorPart := range selectorParts {
		labelSelector, err := parseLabelSelector(selectorPart)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector requirement %d %s: %w", i, selector, err)
		}
		labelSelectors = append(labelSelectors, labelSelector)
	}

	if err := ValidateLabelSelectors(labelSelectors); err != nil {
		return nil, err
	}

	return labelSelectors, nil
}

func splitLabelSelectorParts(selector string) ([]string, error) {
	selectorParts := []string{}
	start := 0
	depth := 0

	for i, char := range selector {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced parentheses in label selector %q", selector)
			}
		case ',':
			if depth == 0 {
				selectorPart := strings.TrimSpace(selector[start:i])
				if selectorPart == "" {
					return nil, fmt.Errorf("empty label selector requirement in %q", selector)
				}
				selectorParts = append(selectorParts, selectorPart)
				start = i + 1
			}
		}
	}

	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parentheses in label selector %q", selector)
	}

	lastSelectorPart := strings.TrimSpace(selector[start:])
	if lastSelectorPart == "" {
		return nil, fmt.Errorf("empty label selector requirement in %q", selector)
	}

	selectorParts = append(selectorParts, lastSelectorPart)
	return selectorParts, nil
}

func parseLabelSelector(selectorPart string) (LabelSelector, error) {
	lowerSelectorPart := strings.ToLower(selectorPart)
	operatorIndex := strings.Index(lowerSelectorPart, " in ")
	if operatorIndex == -1 {
		return LabelSelector{}, fmt.Errorf("must use the IN operator")
	}

	key := strings.TrimSpace(selectorPart[:operatorIndex])
	valuesPart := strings.TrimSpace(selectorPart[operatorIndex+4:])
	if key == "" {
		return LabelSelector{}, fmt.Errorf("key is required")
	}
	if !strings.HasPrefix(valuesPart, "(") || !strings.HasSuffix(valuesPart, ")") {
		return LabelSelector{}, fmt.Errorf("values must be wrapped in parentheses")
	}

	rawValues := strings.TrimSpace(valuesPart[1 : len(valuesPart)-1])
	if rawValues == "" {
		return LabelSelector{
			Key:      key,
			Operator: LabelSelectorOperatorIn,
			Values:   []string{""},
		}, nil
	}

	parsedValues, err := parseLabelSelectorValues(rawValues)
	if err != nil {
		return LabelSelector{}, err
	}

	return LabelSelector{
		Key:      key,
		Operator: LabelSelectorOperatorIn,
		Values:   parsedValues,
	}, nil
}

func normalizeEmptyLabelSelectorValues(values []string) []string {
	if len(values) == 0 {
		return []string{""}
	}

	return values
}

func formatLabelSelectorValue(value string) string {
	if value == "" || strings.ContainsAny(value, `,()"\`) || strings.IndexFunc(value, unicode.IsSpace) != -1 {
		return strconv.Quote(value)
	}

	return value
}

func parseLabelSelectorValues(rawValues string) ([]string, error) {
	parts, err := splitLabelSelectorValues(rawValues)
	if err != nil {
		return nil, err
	}

	parsedValues := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, `"`) || strings.HasSuffix(part, `"`) {
			if !strings.HasPrefix(part, `"`) || !strings.HasSuffix(part, `"`) {
				return nil, fmt.Errorf("quoted values must start and end with '\"'")
			}

			parsedValue, err := strconv.Unquote(part)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted value %q: %w", part, err)
			}

			parsedValues = append(parsedValues, parsedValue)
			continue
		}

		parsedValue := strings.TrimSpace(part)
		if parsedValue == "" {
			return nil, fmt.Errorf("values must not be empty")
		}

		parsedValues = append(parsedValues, parsedValue)
	}

	return parsedValues, nil
}

func splitLabelSelectorValues(rawValues string) ([]string, error) {
	parts := make([]string, 0, 1)
	var currentPart strings.Builder
	inQuotes := false
	escaped := false

	for _, char := range rawValues {
		switch {
		case escaped:
			currentPart.WriteRune(char)
			escaped = false
		case inQuotes && char == '\\':
			currentPart.WriteRune(char)
			escaped = true
		case char == '"':
			currentPart.WriteRune(char)
			inQuotes = !inQuotes
		case char == ',' && !inQuotes:
			part := strings.TrimSpace(currentPart.String())
			if part == "" {
				return nil, fmt.Errorf("values must not be empty")
			}

			parts = append(parts, part)
			currentPart.Reset()
		default:
			currentPart.WriteRune(char)
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("unterminated quoted value")
	}

	if escaped {
		return nil, fmt.Errorf("unterminated escape sequence")
	}

	part := strings.TrimSpace(currentPart.String())
	if part == "" {
		return nil, fmt.Errorf("values must not be empty")
	}

	parts = append(parts, part)
	return parts, nil
}

func validateLabelPartKey(value string) error {
	if value == "" {
		return fmt.Errorf("key must not be empty")
	}

	if utf8.RuneCountInString(value) > maxLabelPartRunes {
		return fmt.Errorf("key must not exceed %d characters", maxLabelPartRunes)
	}

	if strings.Contains(value, "=") {
		return fmt.Errorf("key must not contain '='")
	}
	return nil
}

func validateLabelPartValue(value string) error {
	if utf8.RuneCountInString(value) > maxLabelPartRunes {
		return fmt.Errorf("value must not exceed %d characters", maxLabelPartRunes)
	}

	if strings.Contains(value, "=") {
		return fmt.Errorf("value must not contain '='")
	}
	return nil
}
