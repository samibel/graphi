package skillgen

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// ParseSkill parses a generated Markdown skill artifact back into a Skill value.
// It is intentionally tolerant: it extracts the YAML frontmatter fields and the
// step list so the agent dispatcher can load generated skills without LLM
// parsing.
func ParseSkill(data []byte) (Skill, error) {
	var s Skill
	parts := bytes.SplitN(data, []byte("---\n"), 3)
	if len(parts) < 3 {
		return s, fmt.Errorf("skillgen: missing YAML frontmatter")
	}
	front := parts[1]
	body := parts[2]

	scanner := bufio.NewScanner(bytes.NewReader(front))
	var currentList string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			item := strings.Trim(strings.TrimPrefix(line, "- "), "\"")
			switch currentList {
			case "inputs":
				s.Inputs = append(s.Inputs, item)
			case "outputs":
				s.Outputs = append(s.Outputs, item)
			}
			continue
		}
		currentList = ""
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.Trim(strings.TrimSpace(line[idx+1:]), "\"")
			switch key {
			case "name":
				s.Name = val
			case "trigger":
				s.Trigger = val
			case "description":
				s.Description = val
			case "inputs":
				currentList = "inputs"
				if val != "" && val != "[]" {
					s.Inputs = append(s.Inputs, val)
				}
			case "outputs":
				currentList = "outputs"
				if val != "" && val != "[]" {
					s.Outputs = append(s.Outputs, val)
				}
			}
		}
	}

	// Parse steps from the body.
	scanner = bufio.NewScanner(bytes.NewReader(body))
	inSteps := false
	var current Step
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Steps" {
			inSteps = true
			continue
		}
		if !inSteps {
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if idx := strings.Index(trimmed, ". **"); idx > 0 {
			if current.Name != "" {
				s.Steps = append(s.Steps, current)
			}
			rest := trimmed[idx+4:]
			end := strings.Index(rest, "**")
			if end < 0 {
				continue
			}
			name := rest[:end]
			action := strings.TrimSpace(strings.TrimPrefix(rest[end+2:], "—"))
			current = Step{Name: name, Action: action}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "   - ") {
			item := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "   - "), "- "))
			switch {
			case strings.HasPrefix(item, "Guard: "):
				current.Guard = strings.TrimPrefix(item, "Guard: ")
			case strings.HasPrefix(item, "Input: "):
				current.Inputs = append(current.Inputs, strings.TrimPrefix(item, "Input: "))
			case strings.HasPrefix(item, "Output: "):
				current.Outputs = append(current.Outputs, strings.TrimPrefix(item, "Output: "))
			default:
				current.Description = item
			}
		}
	}
	if current.Name != "" {
		s.Steps = append(s.Steps, current)
	}
	return s, scanner.Err()
}
