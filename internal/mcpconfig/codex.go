package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

const codexHomeEnv = "CODEX_HOME"

func codexConfigPath() (string, error) {
	if configured := os.Getenv(codexHomeEnv); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("mcpconfig: CODEX_HOME must be absolute")
		}
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("mcpconfig: CODEX_HOME must exist: %w", err)
		}
		if !info.IsDir() {
			return "", errors.New("mcpconfig: CODEX_HOME is not a directory")
		}
		return filepath.Join(configured, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("mcpconfig: resolve home for Codex: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

type codexSnapshot struct {
	configSnapshot
	doc map[string]any
}

func loadCodexSnapshot(path string) (codexSnapshot, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return codexSnapshot{configSnapshot: configSnapshot{}, doc: map[string]any{}}, nil
	}
	if err != nil {
		return codexSnapshot{}, fmt.Errorf("mcpconfig: read %s: %w", path, err)
	}
	doc, err := decodeTOML(raw)
	if err != nil {
		return codexSnapshot{}, fmt.Errorf("mcpconfig: parse %s: %w", path, err)
	}
	return codexSnapshot{configSnapshot: configSnapshot{raw: raw, exists: true}, doc: doc}, nil
}

func validateTOML(raw []byte) error {
	_, err := decodeTOML(raw)
	return err
}

func decodeTOML(raw []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func planCodex(path, name string, entry ServerEntry) (Action, error) {
	snapshot, err := loadCodexSnapshot(path)
	if err != nil {
		return "", err
	}
	return planCodexDocument(snapshot.doc, name, entry)
}

func planCodexDocument(doc map[string]any, name string, entry ServerEntry) (Action, error) {
	servers, err := tomlObject(doc, "mcp_servers")
	if err != nil {
		return "", err
	}
	current, exists := servers[name]
	if !exists {
		return ActionCreated, nil
	}
	merged, err := mergeCodexEntry(current, entry)
	if err != nil {
		return "", fmt.Errorf("mcpconfig: Codex server %q: %w", name, err)
	}
	if equalJSON(current, merged) {
		return ActionUnchanged, nil
	}
	return ActionUpdated, nil
}

func planCodexEntryState(path, name string, desired ServerEntry) (EntryState, error) {
	snapshot, err := loadCodexSnapshot(path)
	if err != nil {
		return EntryState{}, err
	}
	servers, err := tomlObject(snapshot.doc, "mcp_servers")
	if err != nil {
		return EntryState{}, err
	}
	current, exists := servers[name]
	var target any = map[string]any{"command": desired.Command, "args": append([]string(nil), desired.Args...)}
	if len(desired.Env) > 0 {
		env := make(map[string]any, len(desired.Env))
		for key, value := range desired.Env {
			env[key] = value
		}
		target.(map[string]any)["env"] = env
	}
	action := ActionCreated
	if exists {
		target, err = mergeCodexEntry(current, desired)
		if err != nil {
			return EntryState{}, err
		}
		action = ActionUpdated
		if equalJSON(current, target) {
			action = ActionUnchanged
		}
	}
	beforeDigest := configDigest(snapshot.raw)
	targetDigest := beforeDigest
	if action != ActionUnchanged {
		updated, editErr := editCodexTOML(snapshot.raw, snapshot.doc, name, desired)
		if editErr != nil {
			return EntryState{}, editErr
		}
		if err := validateTOML(updated); err != nil {
			return EntryState{}, err
		}
		targetDigest = configDigest(updated)
	}
	return EntryState{
		Action: action, Current: current, Target: target,
		BeforeConfigDigest: beforeDigest, TargetConfigDigest: targetDigest,
	}, nil
}

func tomlObject(doc map[string]any, key string) (map[string]any, error) {
	raw, exists := doc[key]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcpconfig: %s must be a TOML table", key)
	}
	return object, nil
}

func mergeCodexEntry(current any, desired ServerEntry) (map[string]any, error) {
	object, ok := current.(map[string]any)
	if !ok {
		return nil, errors.New("existing entry must be a TOML table")
	}
	merged := make(map[string]any, len(object)+3)
	for key, value := range object {
		merged[key] = value
	}
	merged["command"] = desired.Command
	merged["args"] = append([]string(nil), desired.Args...)
	if len(desired.Env) > 0 {
		env := map[string]any{}
		if existing, exists := merged["env"]; exists {
			var envOK bool
			env, envOK = existing.(map[string]any)
			if !envOK {
				return nil, errors.New("existing env must be a TOML table")
			}
			copyEnv := make(map[string]any, len(env)+len(desired.Env))
			for key, value := range env {
				if _, ok := value.(string); !ok {
					return nil, fmt.Errorf("existing env key %q is not a string", key)
				}
				copyEnv[key] = value
			}
			env = copyEnv
		}
		for key, value := range desired.Env {
			env[key] = value
		}
		merged["env"] = env
	}
	return merged, nil
}

func applyCodex(path, name string, entry ServerEntry, dryRun bool) (Result, error) {
	if strings.TrimSpace(name) == "" {
		return Result{}, errors.New("mcpconfig: empty Codex server name")
	}
	if dryRun {
		action, err := planCodex(path, name, entry)
		if err != nil {
			return Result{}, err
		}
		return Result{Action: action, Diff: redactedDiff(name, action, entry)}, nil
	}

	mu := pathMutex(path)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Result{}, fmt.Errorf("mcpconfig: mkdir Codex config: %w", err)
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	snapshot, err := loadCodexSnapshot(path)
	if err != nil {
		return Result{}, err
	}
	action, err := planCodexDocument(snapshot.doc, name, entry)
	if err != nil {
		return Result{}, err
	}
	diff := redactedDiff(name, action, entry)
	if action == ActionUnchanged {
		return Result{Action: action, Diff: diff}, nil
	}
	updated, err := editCodexTOML(snapshot.raw, snapshot.doc, name, entry)
	if err != nil {
		return Result{}, err
	}
	backupPath, err := writeAtomicBytesWithBackupSnapshot(path, updated, snapshot.configSnapshot, writerHooks{}, validateTOML)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: action, Diff: diff, BackupPath: backupPath}, nil
}

func removeCodex(path, name string, dryRun bool) (Result, error) {
	snapshot, err := loadCodexSnapshot(path)
	if err != nil {
		return Result{}, err
	}
	servers, err := tomlObject(snapshot.doc, "mcp_servers")
	if err != nil {
		return Result{}, err
	}
	if _, exists := servers[name]; !exists {
		return Result{Action: ActionUnchanged, Diff: redactedDiff(name, ActionUnchanged, ServerEntry{})}, nil
	}
	if dryRun {
		return Result{Action: ActionRemoved, Diff: redactedDiff(name, ActionRemoved, ServerEntry{})}, nil
	}

	mu := pathMutex(path)
	mu.Lock()
	defer mu.Unlock()
	unlock, err := lockConfig(path)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	snapshot, err = loadCodexSnapshot(path)
	if err != nil {
		return Result{}, err
	}
	servers, err = tomlObject(snapshot.doc, "mcp_servers")
	if err != nil {
		return Result{}, err
	}
	if _, exists := servers[name]; !exists {
		return Result{Action: ActionUnchanged, Diff: redactedDiff(name, ActionUnchanged, ServerEntry{})}, nil
	}
	layout, err := inspectCodexLayout(snapshot.raw, name)
	if err != nil {
		return Result{}, err
	}
	if !layout.tableFound || layout.tableStart < 0 || layout.tableEnd < layout.tableStart {
		return Result{}, fmt.Errorf("mcpconfig: Codex server %q is not an editable explicit table", name)
	}
	updated := append([]byte(nil), snapshot.raw[:layout.tableStart]...)
	updated = append(updated, snapshot.raw[layout.tableEnd:]...)
	backupPath, err := writeAtomicBytesWithBackupSnapshot(path, updated, snapshot.configSnapshot, writerHooks{}, validateTOML)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: ActionRemoved, Diff: redactedDiff(name, ActionRemoved, ServerEntry{}), BackupPath: backupPath}, nil
}

type tomlValueEdit struct {
	start int
	end   int
	value []byte
}

type codexLayout struct {
	tableFound  bool
	tableStart  int
	tableEnd    int
	headerEnd   int
	valueRanges map[string]unstable.Range
	nestedEnv   bool
}

func editCodexTOML(raw []byte, doc map[string]any, name string, desired ServerEntry) ([]byte, error) {
	layout, err := inspectCodexLayout(raw, name)
	if err != nil {
		return nil, err
	}
	servers, err := tomlObject(doc, "mcp_servers")
	if err != nil {
		return nil, err
	}
	current, exists := servers[name]
	if exists && !layout.tableFound {
		return nil, fmt.Errorf("mcpconfig: Codex server %q is not an editable explicit table", name)
	}
	if !exists {
		return appendCodexTable(raw, name, desired), nil
	}
	merged, err := mergeCodexEntry(current, desired)
	if err != nil {
		return nil, err
	}
	values := map[string][]byte{
		"command": tomlString(desired.Command),
		"args":    tomlStringArray(desired.Args),
	}
	if len(desired.Env) > 0 {
		if layout.nestedEnv && layout.valueRanges["env"].Length == 0 {
			return nil, errors.New("mcpconfig: nested Codex env tables require explicit migration")
		}
		env, ok := merged["env"].(map[string]any)
		if !ok {
			return nil, errors.New("mcpconfig: Codex env is not a table")
		}
		values["env"] = tomlInlineStringMap(env)
	}

	var edits []tomlValueEdit
	var missing []string
	for _, key := range []string{"command", "args", "env"} {
		value, wanted := values[key]
		if !wanted {
			continue
		}
		if rawRange, found := layout.valueRanges[key]; found {
			edits = append(edits, tomlValueEdit{start: int(rawRange.Offset), end: int(rawRange.Offset + rawRange.Length), value: value})
		} else {
			missing = append(missing, key)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), raw...)
	for _, edit := range edits {
		out = append(out[:edit.start], append(edit.value, out[edit.end:]...)...)
	}
	if len(missing) > 0 {
		// Existing-value replacements can occur only after the header, so its
		// insertion offset is stable.
		var block bytes.Buffer
		for _, key := range missing {
			fmt.Fprintf(&block, "%s = %s\n", key, values[key])
		}
		out = append(out[:layout.headerEnd], append(block.Bytes(), out[layout.headerEnd:]...)...)
	}
	return out, nil
}

func inspectCodexLayout(raw []byte, name string) (codexLayout, error) {
	layout := codexLayout{valueRanges: map[string]unstable.Range{}}
	parser := unstable.Parser{}
	parser.Reset(raw)
	var current []string
	target := []string{"mcp_servers", name}
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table:
			nextTable := nodeKey(expression)
			start := tableLineStart(raw, expression)
			if layout.tableFound && layout.tableEnd == 0 && !pathHasPrefix(nextTable, target) {
				layout.tableEnd = start
			}
			current = nextTable
			if reflect.DeepEqual(current, target) {
				if layout.tableFound {
					return codexLayout{}, fmt.Errorf("mcpconfig: duplicate Codex server table %q", name)
				}
				layout.tableFound = true
				layout.tableStart = start
				first := expression.Key()
				if !first.Next() {
					return codexLayout{}, errors.New("mcpconfig: empty TOML table key")
				}
				layout.headerEnd = lineEnd(raw, int(first.Node().Raw.Offset))
			}
			if reflect.DeepEqual(current, append(append([]string(nil), target...), "env")) {
				layout.nestedEnv = true
			}
		case unstable.ArrayTable:
			nextTable := nodeKey(expression)
			if layout.tableFound && layout.tableEnd == 0 && !pathHasPrefix(nextTable, target) {
				layout.tableEnd = tableLineStart(raw, expression)
			}
			current = nextTable
		case unstable.KeyValue:
			if !reflect.DeepEqual(current, target) {
				continue
			}
			key := nodeKey(expression)
			if len(key) == 1 && (key[0] == "command" || key[0] == "args" || key[0] == "env") {
				valueRange, rangeErr := keyValueRange(raw, expression)
				if rangeErr != nil {
					return codexLayout{}, rangeErr
				}
				layout.valueRanges[key[0]] = valueRange
			}
		}
	}
	if err := parser.Error(); err != nil {
		return codexLayout{}, fmt.Errorf("mcpconfig: parse Codex TOML layout: %w", err)
	}
	if layout.tableFound && layout.tableEnd == 0 {
		layout.tableEnd = len(raw)
	}
	return layout, nil
}

func tableLineStart(raw []byte, expression *unstable.Node) int {
	iterator := expression.Key()
	if !iterator.Next() {
		return 0
	}
	offset := int(iterator.Node().Raw.Offset)
	if offset > len(raw) {
		return len(raw)
	}
	if index := bytes.LastIndexByte(raw[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func pathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	return reflect.DeepEqual(path[:len(prefix)], prefix)
}

func keyValueRange(raw []byte, expression *unstable.Node) (unstable.Range, error) {
	root := expression.Raw
	rootEnd := int(root.Offset + root.Length)
	lastKeyEnd := int(root.Offset)
	iterator := expression.Key()
	for iterator.Next() {
		keyRange := iterator.Node().Raw
		lastKeyEnd = int(keyRange.Offset + keyRange.Length)
	}
	if lastKeyEnd > rootEnd || rootEnd > len(raw) {
		return unstable.Range{}, errors.New("mcpconfig: invalid TOML key/value range")
	}
	equals := bytes.IndexByte(raw[lastKeyEnd:rootEnd], '=')
	if equals < 0 {
		return unstable.Range{}, errors.New("mcpconfig: TOML key/value has no separator")
	}
	start := lastKeyEnd + equals + 1
	for start < rootEnd && (raw[start] == ' ' || raw[start] == '\t') {
		start++
	}
	return unstable.Range{Offset: uint32(start), Length: uint32(rootEnd - start)}, nil
}

func nodeKey(node *unstable.Node) []string {
	var key []string
	iterator := node.Key()
	for iterator.Next() {
		key = append(key, string(iterator.Node().Data))
	}
	return key
}

func lineEnd(raw []byte, offset int) int {
	if offset < 0 || offset > len(raw) {
		return len(raw)
	}
	if index := bytes.IndexByte(raw[offset:], '\n'); index >= 0 {
		return offset + index + 1
	}
	return len(raw)
}

func appendCodexTable(raw []byte, name string, entry ServerEntry) []byte {
	out := append([]byte(nil), raw...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if len(out) > 0 && len(bytes.TrimSpace(out)) > 0 {
		out = append(out, '\n')
	}
	header := "[mcp_servers." + tomlKey(name) + "]\n"
	out = append(out, header...)
	out = append(out, "command = "...)
	out = append(out, tomlString(entry.Command)...)
	out = append(out, '\n')
	out = append(out, "args = "...)
	out = append(out, tomlStringArray(entry.Args)...)
	out = append(out, '\n')
	if len(entry.Env) > 0 {
		env := make(map[string]any, len(entry.Env))
		for key, value := range entry.Env {
			env[key] = value
		}
		out = append(out, "env = "...)
		out = append(out, tomlInlineStringMap(env)...)
		out = append(out, '\n')
	}
	return out
}

func tomlKey(key string) string {
	if key != "" {
		bare := true
		for _, char := range key {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
				bare = false
				break
			}
		}
		if bare {
			return key
		}
	}
	return string(tomlString(key))
}

func tomlString(value string) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func tomlStringArray(values []string) []byte {
	var out bytes.Buffer
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteString(", ")
		}
		out.Write(tomlString(value))
	}
	out.WriteByte(']')
	return out.Bytes()
}

func tomlInlineStringMap(values map[string]any) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	out.WriteString("{ ")
	for index, key := range keys {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(tomlKey(key))
		out.WriteString(" = ")
		text, ok := values[key].(string)
		if !ok {
			// mergeCodexEntry only admits string values from ServerEntry. A
			// foreign non-string value remains a safe validation error.
			text = fmt.Sprint(values[key])
		}
		out.Write(tomlString(text))
	}
	out.WriteString(" }")
	return out.Bytes()
}
