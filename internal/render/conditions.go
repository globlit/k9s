// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/globlit/k9s/internal/model1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// readyFromConditions returns a diagnostic and whether the object provides
// recognized readiness evidence. Missing status is not evidence of failure.
func readyFromConditions(obj map[string]any) (string, bool) {
	value, _, _ := unstructured.NestedFieldNoCopy(obj, "status", "conditions")
	conditions, _ := value.([]any)
	generation, _, _ := unstructured.NestedInt64(obj, "metadata", "generation")
	observed, found, _ := unstructured.NestedInt64(obj, "status", "observedGeneration")
	if found && observed < generation {
		return "Reconciling", true
	}
	known := false
	for _, value := range conditions {
		condition, ok := value.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := condition["type"].(string)
		switch typ {
		case "Ready", "Available", "Synced", "Healthy":
		default:
			continue
		}
		status, _ := condition["status"].(string)
		if status == "" {
			continue
		}
		known = true
		if observed, ok := condition["observedGeneration"].(int64); ok && observed < generation {
			return "Reconciling", true
		}
		if status == "True" {
			continue
		}
		reason, _ := condition["reason"].(string)
		message, _ := condition["message"].(string)
		detail := reason
		if message != "" {
			if detail != "" {
				detail += ": "
			}
			detail += message
		}
		if detail == "" {
			detail = status
		}
		return typ + ": " + detail, true
	}
	return "", known
}

func readyFromCells(header model1.Header, cells []any) string {
	for i, col := range header {
		if i >= len(cells) || cells[i] == nil {
			continue
		}
		switch strings.ToUpper(col.Name) {
		case "READY", "SYNCED", "AVAILABLE", "HEALTHY", "STATUS":
		default:
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(cells[i]))
		lower := strings.ToLower(value)
		invalid := lower == "false" || lower == "unknown"
		for _, word := range strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) }) {
			switch word {
			case "failed", "error", "degraded", "stalled", "unhealthy":
				invalid = true
			}
		}
		if strings.EqualFold(col.Name, "READY") {
			numerator, denominator, ok := strings.Cut(value, "/")
			if ok {
				ready, e1 := strconv.ParseUint(strings.TrimSpace(numerator), 10, 64)
				desired, e2 := strconv.ParseUint(strings.TrimSpace(denominator), 10, 64)
				invalid = invalid || e1 == nil && e2 == nil && ready == 0 && desired > 0
			}
		}
		if invalid {
			return col.Name + ": " + value
		}
	}
	return ""
}
