// Prompt re-read for Codex: resolve the human prompt behind a turn by reading the
// original rollout .jsonl on demand. As with Claude Code, aispend stores no
// prompt text and hashes source paths, so the explain view recovers the prompt live
// from the user's own log — nothing new is persisted. Local, read-only file access
// (no network), so the offline build is untouched.
package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
)

// promptLookback caps how many records before a turn we scan back for its prompt —
// the human turn is almost always immediately before (or a few tool/assistant lines
// back), so a bounded window keeps memory flat on deep rollouts.
const promptLookback = 400

// PromptBefore returns the human prompt preceding the billable turn at assistantLine
// (the 1-indexed physical line recorded as Evidence.SourceLine — for Codex, the
// token_count line) in the rollout file at path. It scans back to the nearest
// response_item user message. ok is false — never an error — when the file can't be
// read, the line is out of range, or no such prompt exists.
func PromptBefore(path string, assistantLine int) (string, bool) {
	if assistantLine < 2 {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	r := bufio.NewReader(f)
	window := make([][]byte, 0, promptLookback)
	for i := 1; i < assistantLine; i++ {
		line, rerr := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if len(window) == promptLookback {
				window = window[1:]
			}
			window = append(window, append([]byte(nil), t...))
		}
		if rerr != nil {
			break
		}
	}
	return promptBefore(window)
}

// promptBefore walks the preceding records newest-first and returns the first one
// that is a human prompt — skipping assistant messages, tool calls, and event_msgs.
func promptBefore(preceding [][]byte) (string, bool) {
	for j := len(preceding) - 1; j >= 0; j-- {
		if txt, ok := userPromptText(preceding[j]); ok {
			return txt, true
		}
	}
	return "", false
}

// userPromptText extracts the typed text from one rollout record if it is a user
// message: a top-level response_item whose payload is {type:"message", role:"user"}
// with content as a plain string or "input_text"/"text" blocks. Assistant messages,
// function_call payloads, and event_msg lines carry no typed prompt.
func userPromptText(raw []byte) (string, bool) {
	var line struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &line); err != nil {
		return "", false
	}
	if line.Type != "response_item" || len(line.Payload) == 0 {
		return "", false
	}

	var msg struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line.Payload, &msg); err != nil {
		return "", false
	}
	if msg.Type != "message" || msg.Role != "user" || len(msg.Content) == 0 {
		return "", false
	}

	// content is either a JSON string or an array of typed blocks.
	var asString string
	if err := json.Unmarshal(msg.Content, &asString); err == nil {
		if s := strings.TrimSpace(asString); s != "" {
			return s, true
		}
		return "", false
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			// Codex user input is "input_text"; accept "text" too, defensively.
			if b.Type == "input_text" || b.Type == "text" {
				if s := strings.TrimSpace(b.Text); s != "" {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), true
		}
	}
	return "", false
}
