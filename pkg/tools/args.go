package tools

import (
	"fmt"
	"math"
	"strings"
)

func parseIntArg(args map[string]any, key string, defaultVal, minVal, maxVal int) (int, error) {
	val, exists := args[key]
	if !exists {
		return defaultVal, nil
	}

	n, err := toInt(val)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if n < minVal || n > maxVal {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minVal, maxVal)
	}
	return n, nil
}

func parseOptionalIntArg(args map[string]any, key string, defaultVal, minVal, maxVal int) (int, error) {
	val, exists := args[key]
	if !exists {
		return defaultVal, nil
	}

	n, err := toInt(val)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if n < minVal || n > maxVal {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minVal, maxVal)
	}
	return n, nil
}

func parseBoolArg(args map[string]any, key string, defaultVal bool) (bool, error) {
	val, exists := args[key]
	if !exists {
		return defaultVal, nil
	}
	b, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}

func parseStringSliceArg(args map[string]any, key string) ([]string, error) {
	val, exists := args[key]
	if !exists {
		return nil, nil
	}

	raw, ok := val.([]any)
	if !ok {
		if s, ok := val.([]string); ok {
			out := make([]string, 0, len(s))
			for _, item := range s {
				item = strings.TrimSpace(item)
				if item != "" {
					out = append(out, item)
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of strings", key)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func getStringArg(args map[string]any, key string) (string, bool) {
	val, exists := args[key]
	if !exists {
		return "", false
	}
	s, ok := val.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func toInt(v any) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int32:
		return int(t), nil
	case int64:
		if t > math.MaxInt || t < math.MinInt {
			return 0, fmt.Errorf("out of range")
		}
		return int(t), nil
	case float64:
		// JSON numbers decode as float64.
		if t > float64(math.MaxInt) || t < float64(math.MinInt) {
			return 0, fmt.Errorf("out of range")
		}
		return int(t), nil
	default:
		return 0, fmt.Errorf("invalid type")
	}
}
