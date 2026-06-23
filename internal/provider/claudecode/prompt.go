// Prompt re-read: resolve the human prompt behind a turn by reading the original
// session .jsonl on demand. aispend stores no prompt text and hashes source
// paths, so the explain view recovers the prompt live from the user's own log —
// nothing new is persisted. This is a local, read-only file access (no network),
// so the offline build and `doctor --network` promise are untouched.
package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
)

// promptLookback caps how many records before an assistant turn we scan back for
// its prompt. The human turn is almost always immediately before (or a few
// tool_result lines back), so a bounded window keeps memory flat on deep sessions
// while still finding the prompt in practice.
const promptLookback = 400

// PromptBefore returns the human prompt that precedes the assistant turn at
// assistantLine (the 1-indexed physical line recorded as Evidence.SourceLine) in
// the session file at path. It scans back over the preceding records to the nearest
// type:"user" line carrying typed text. ok is false — never an error — when the file
// can't be read, the line is out of range, or no such prompt exists, so the caller
// can degrade gracefully.
func PromptBefore(path string, assistantLine int) (string, bool) {
	if assistantLine < 2 { // nothing precedes the first line
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	// Keep a sliding window of the last promptLookback non-empty lines strictly
	// before the assistant turn. bufio.Reader (not Scanner) tolerates the very long
	// lines real sessions embed (large tool outputs on one line).
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
// that is a human prompt — skipping assistant turns and tool_result-only user lines.
func promptBefore(preceding [][]byte) (string, bool) {
	for j := len(preceding) - 1; j >= 0; j-- {
		if txt, ok := userPromptText(preceding[j]); ok {
			return txt, true
		}
	}
	return "", false
}

// userPromptText extracts the typed text from one record if it is a type:"user"
// turn carrying human input: message.content as a plain string, or the joined
// "text" blocks of an array content. tool_result blocks (tool output echoed back as
// a user turn) carry no typed text, so a tool_result-only line returns false.
func userPromptText(raw []byte) (string, bool) {
	var rec struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", false
	}
	if rec.Type != "user" || len(rec.Message.Content) == 0 {
		return "", false
	}

	// content is either a JSON string or an array of typed blocks.
	var asString string
	if err := json.Unmarshal(rec.Message.Content, &asString); err == nil {
		if s := strings.TrimSpace(asString); s != "" {
			return s, true
		}
		return "", false
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Message.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
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
