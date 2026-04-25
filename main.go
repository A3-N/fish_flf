package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// ANSI helpers — used only for our own framing, never alter log output
const (
	cReset   = "\033[0m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
	cReverse = "\033[7m"
	cCyan    = "\033[36m"
	cGreen   = "\033[32m"
	cRed     = "\033[31m"
	cYellow  = "\033[33m"
	cGray    = "\033[90m"
	cBgSel   = "\033[48;5;255m" // white background for selected row
	cFgSel   = "\033[30m"       // black foreground for selected row

	escClearScreen  = "\033[2J"
	escClearLine    = "\033[2K"
	escCursorHome   = "\033[H"
	escHideCursor   = "\033[?25l"
	escShowCursor   = "\033[?25h"
)

// logDir is where kitty scrollback saves go (via ctrl+shift+s)
const logDir = "~/.config/fish/logs"

// configDir holds the flf configuration (e.g. detected prompt delimiter)
const configDir = "~/.config/fish"

// ansiRegex strips escape sequences for search matching only — display is untouched
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)

// promptDelimiters is a list of common shell prompt characters, tried in order.
// The configured delimiter is prepended at startup.
var promptDelimiters = []string{"►", "❯", "❱", "➜", "λ", "$", "#", "%", ">"}

// CommandBlock is a single command + its output from a log file.
type CommandBlock struct {
	File        string
	LineNum     int
	Command     string // ANSI-stripped command text (for filtering)
	RawCommand  string // original ANSI-colored command (for display)
	StartByte   int64  // start offset in file
	EndByte     int64  // end offset in file
	MatchedLine string // matched output line (if any)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	loadPromptConfig()

	searchOutput := false
	args := os.Args[1:]

	if len(args) > 0 && (args[0] == "-o" || args[0] == "--output") {
		searchOutput = true
		args = args[1:]
	}

	if len(args) == 0 {
		runInteractive(searchOutput)
	} else {
		runSearch(strings.Join(args, " "))
	}
}

// loadPromptConfig reads the prompt delimiter from ~/.config/flf/prompt.
// If found, it becomes the primary delimiter used for parsing and display.
func loadPromptConfig() {
	path := filepath.Join(expandHome(configDir), "prompt")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	d := strings.TrimSpace(string(data))
	if d == "" {
		return
	}
	// Prepend configured delimiter so it's tried first
	promptDelimiters = append([]string{d}, promptDelimiters...)
}

// ---------------------------------------------------------------------------
// path helpers
// ---------------------------------------------------------------------------

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// ---------------------------------------------------------------------------
// log file collection + parsing (shared by both modes)
// ---------------------------------------------------------------------------

// collectLogFiles gathers .log files sorted oldest-first (newest last).
func collectLogFiles() []string {
	seen := make(map[string]bool)
	var files []string

	addFrom := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".log") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			abs, err := filepath.Abs(full)
			if err != nil {
				abs = full
			}
			if !seen[abs] {
				seen[abs] = true
				files = append(files, abs)
			}
		}
	}

	addFrom(expandHome(logDir))

	sort.Slice(files, func(i, j int) bool {
		fi, _ := os.Stat(files[i])
		fj, _ := os.Stat(files[j])
		if fi == nil || fj == nil {
			return files[i] < files[j]
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	return files
}

func stripANSI(s string) string { return ansiRegex.ReplaceAllString(s, "") }

// isPromptLine detects prompt lines using the OSC 133;A shell integration
// marker that fish/zsh/bash emit. Works regardless of prompt appearance.
func isPromptLine(rawLine string) bool {
	return strings.Contains(rawLine, "\x1b]133;A")
}

// findPromptDelimiter locates the first known prompt delimiter in a cleaned line.
// Returns the index and the delimiter string, or -1 if none found.
func findPromptDelimiter(clean string) (int, string) {
	for _, d := range promptDelimiters {
		if idx := strings.Index(clean, d); idx >= 0 {
			return idx, d
		}
	}
	return -1, ""
}

func extractCommand(clean string) string {
	idx, delim := findPromptDelimiter(clean)
	if idx < 0 {
		// No known delimiter — treat the whole stripped line as the command
		return strings.TrimSpace(clean)
	}
	cmd := strings.TrimSpace(clean[idx+len(delim):])

	// Strip trailing HH:MM timestamp
	if len(cmd) >= 5 {
		t := cmd[len(cmd)-5:]
		if t[2] == ':' &&
			t[0] >= '0' && t[0] <= '2' && t[1] >= '0' && t[1] <= '9' &&
			t[3] >= '0' && t[3] <= '5' && t[4] >= '0' && t[4] <= '9' {
			cmd = strings.TrimSpace(cmd[:len(cmd)-5])
		}
	}

	// Strip trailing duration like "0.344s"
	parts := strings.Fields(cmd)
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		if strings.HasSuffix(last, "s") {
			num := last[:len(last)-1]
			ok, dot := true, false
			for _, c := range num {
				if c == '.' {
					dot = true
				} else if c < '0' || c > '9' {
					ok = false
					break
				}
			}
			if ok && dot {
				cmd = strings.Join(parts[:len(parts)-1], " ")
			}
		}
	}

	return strings.TrimSpace(cmd)
}

func parseLogFile(path string) []CommandBlock {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var blocks []CommandBlock
	reader := bufio.NewReader(f)

	var cur *CommandBlock
	var currentOffset int64 = 0
	lineNum := 1

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			raw := string(lineBytes)
			rawTrimmed := strings.TrimRight(raw, "\r\n") // just for prompt checking
			clean := stripANSI(rawTrimmed)

			if isPromptLine(rawTrimmed) {
				if cur != nil {
					cur.EndByte = currentOffset
					if cur.Command != "" {
						blocks = append(blocks, *cur)
					}
				}
				cmd := extractCommand(clean)
				rawCmd := extractRawCommand(rawTrimmed)
				cur = &CommandBlock{
					File:       path,
					LineNum:    lineNum,
					Command:    cmd,
					RawCommand: rawCmd,
					StartByte:  currentOffset,
				}
			}
			currentOffset += int64(len(lineBytes))
			lineNum++
		}
		if err != nil {
			break
		}
	}

	if cur != nil {
		cur.EndByte = currentOffset
		if cur.Command != "" {
			blocks = append(blocks, *cur)
		}
	}

	return blocks
}

func readBlockLines(b CommandBlock) []string {
	f, err := os.Open(b.File)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(b.StartByte, 0); err != nil {
		return nil
	}
	size := b.EndByte - b.StartByte
	if size <= 0 {
		return nil
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
}

func blockContains(b CommandBlock, qLower string) bool {
	if strings.Contains(strings.ToLower(b.Command), qLower) {
		return true
	}
	lines := readBlockLines(b)
	for _, l := range lines {
		if strings.Contains(strings.ToLower(stripANSI(l)), qLower) {
			return true
		}
	}
	return false
}

// displayBlock prints a file/line header, then cats the raw lines as-is.
func displayBlock(block CommandBlock) {
	name := filepath.Base(block.File)
	fmt.Printf("%s%s── %s%s %s(line %d)%s\n",
		cBold, cCyan, name, cReset,
		cDim, block.LineNum, cReset)
	for _, line := range readBlockLines(block) {
		fmt.Println(line)
	}
}

// ---------------------------------------------------------------------------
// grep search mode  (flf <query>)
// ---------------------------------------------------------------------------

func runSearch(query string) {
	logFiles := collectLogFiles()
	if len(logFiles) == 0 {
		fmt.Printf("%s%sNo log files found in %s%s\n", cBold, cRed, expandHome(logDir), cReset)
		os.Exit(1)
	}

	qLower := strings.ToLower(query)
	var matches []CommandBlock
	allBlocks := getAllBlocks()
	for _, b := range allBlocks {
		if blockContains(b, qLower) {
			matches = append(matches, b)
		}
	}

	if len(matches) == 0 {
		fmt.Printf("\n%s%s  No matches found for: %s%s\n\n", cBold, cRed, query, cReset)
		os.Exit(0)
	}

	fmt.Printf("\n%s%s  Found %d match(es) for: %s%s%s\n\n", cBold, cGreen, len(matches), cCyan, query, cReset)
	for i, m := range matches {
		displayBlock(m)
		if i < len(matches)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
}

// ---------------------------------------------------------------------------
// interactive TUI mode  (flf  — no args)
// ---------------------------------------------------------------------------

var (
	fileBytes      map[string][]byte
	fileBytesLower map[string][]byte
)

func getAllBlocks() []CommandBlock {
	files := collectLogFiles()
	results := make([][]CommandBlock, len(files))
	var wg sync.WaitGroup

	for i, f := range files {
		wg.Add(1)
		go func(idx int, path string) {
			defer wg.Done()
			results[idx] = parseLogFile(path)
		}(i, f)
	}
	wg.Wait()

	var all []CommandBlock
	for _, blocks := range results {
		all = append(all, blocks...)
	}
	return all
}

func filterBlocks(blocks []CommandBlock, query string, searchOutput bool) []CommandBlock {
	if query == "" {
		for i := range blocks {
			blocks[i].MatchedLine = ""
		}
		return blocks
	}
	qLower := strings.ToLower(query)
	qBytes := []byte(qLower)
	var out []CommandBlock

	for _, b := range blocks {
		b.MatchedLine = ""
		if strings.Contains(strings.ToLower(b.Command), qLower) {
			out = append(out, b)
			continue
		}

		if searchOutput {
			lowerData := fileBytesLower[b.File]
			if lowerData != nil && b.StartByte < int64(len(lowerData)) && b.EndByte <= int64(len(lowerData)) {
				chunkLower := lowerData[b.StartByte:b.EndByte]
				idx := bytes.Index(chunkLower, qBytes)
				if idx != -1 {
					chunk := fileBytes[b.File][b.StartByte:b.EndByte]
					// Find start of line
					start := idx
					for start > 0 && chunk[start-1] != '\n' {
						start--
					}
					// Find end of line
					end := idx
					for end < len(chunk) && chunk[end] != '\n' && chunk[end] != '\r' {
						end++
					}
					lineStr := string(chunk[start:end])
					b.MatchedLine = strings.TrimSpace(stripANSI(lineStr))
					out = append(out, b)
				}
			}
		}
	}
	return out
}

// readKey reads one keypress and returns a logical name.
func readKey() string {
	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return ""
	}
	b := buf[:n]

	if n == 1 {
		switch b[0] {
		case 27:
			return "esc"
		case 13:
			return "enter"
		case 127, 8:
			return "backspace"
		case 3:
			return "ctrl-c"
		case 14:
			return "ctrl-n"
		case 16:
			return "ctrl-p"
		case 21:
			return "ctrl-u"
		}
		if b[0] >= 32 {
			return string(b)
		}
		return ""
	}

	// Escape sequences (arrow keys)
	if b[0] == 27 && n >= 3 && b[1] == '[' {
		switch b[2] {
		case 'A':
			return "up"
		case 'B':
			return "down"
		}
		return ""
	}

	// Multi-byte UTF-8 printable
	if b[0] >= 32 {
		return string(b)
	}
	return ""
}



// truncate a plain string to fit a given width.
func truncStr(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 3 {
		return string(runes[:maxW])
	}
	return string(runes[:maxW-1]) + "…"
}

// extractRawCommand extracts the colored command portion from the raw prompt line.
// It finds the prompt delimiter and returns everything after it, with ANSI preserved.
func extractRawCommand(rawLine string) string {
	for _, d := range promptDelimiters {
		if idx := strings.Index(rawLine, d); idx >= 0 {
			after := rawLine[idx+len(d):]
			if len(after) > 0 && after[0] == ' ' {
				after = after[1:]
			}
			return after
		}
	}
	return rawLine
}

// visibleLen counts visible characters in a string, ignoring ANSI escape sequences.
func visibleLen(s string) int {
	return len([]rune(stripANSI(s)))
}

// truncANSI truncates a string that may contain ANSI escape sequences
// to a maximum visible width, preserving color codes and appending a reset.
func truncANSI(s string, maxW int) string {
	if visibleLen(s) <= maxW {
		return s + cReset
	}

	showW := maxW - 1
	if showW < 1 {
		showW = 1
	}

	var out strings.Builder
	vis := 0
	runes := []rune(s)
	i := 0

	for i < len(runes) && vis < showW {
		if runes[i] == '\x1b' {
			// Copy the entire escape sequence without counting width
			j := i + 1
			if j < len(runes) && runes[j] == '[' {
				// CSI sequence: ESC [ ... letter
				j++
				for j < len(runes) && !((runes[j] >= 'A' && runes[j] <= 'Z') || (runes[j] >= 'a' && runes[j] <= 'z')) {
					j++
				}
				if j < len(runes) {
					j++
				}
			} else if j < len(runes) && runes[j] == ']' {
				// OSC sequence: ESC ] ... (BEL or ST)
				j++
				for j < len(runes) && runes[j] != '\x07' {
					if runes[j] == '\x1b' && j+1 < len(runes) && runes[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(runes) && runes[j] == '\x07' {
					j++
				}
			} else {
				j++
			}
			out.WriteString(string(runes[i:j]))
			i = j
			continue
		}

		out.WriteRune(runes[i])
		vis++
		i++
	}

	out.WriteString("…")
	out.WriteString(cReset)
	return out.String()
}

func runInteractive(searchOutput bool) {
	blocks := getAllBlocks()

	if searchOutput {
		fileBytes = make(map[string][]byte)
		fileBytesLower = make(map[string][]byte)
		var mu sync.Mutex
		var wg sync.WaitGroup
		files := collectLogFiles()
		for _, f := range files {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				data, err := os.ReadFile(path)
				if err == nil {
					lower := bytes.ToLower(data)
					mu.Lock()
					fileBytes[path] = data
					fileBytesLower[path] = lower
					mu.Unlock()
				}
			}(f)
		}
		wg.Wait()
	}

	if len(blocks) == 0 {
		fmt.Fprintf(os.Stderr, "%s%s  No commands found in log files.%s\n", cBold, cRed, cReset)
		os.Exit(1)
	}

	// Newest first: reverse the slice (collectLogFiles returns oldest-first)
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}

	// Remove blocks with empty commands
	var clean []CommandBlock
	for _, b := range blocks {
		if b.Command != "" {
			clean = append(clean, b)
		}
	}
	blocks = clean

	// Raw terminal mode
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to enter raw mode: %v\n", err)
		os.Exit(1)
	}

	restored := false
	restore := func() {
		if !restored {
			fmt.Print(escShowCursor)
			_ = term.Restore(fd, oldState)
			restored = true
		}
	}
	defer restore()

	// Handle signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		restore()
		os.Exit(0)
	}()

	query := ""
	sel := 0
	offset := 0

	fmt.Print(escHideCursor)
	fmt.Print("\r\n") // Move down 1 line to leave the user's prompt intact

	linesDrawn := 0

	for {
		w, h, _ := term.GetSize(fd)
		if w < 20 {
			w = 80
		}
		if h < 6 {
			h = 24
		}
		w-- // Prevent wrapping when printing exactly on the right edge

		filtered := filterBlocks(blocks, query, searchOutput)
		total := len(blocks)
		shown := len(filtered)

		if shown == 0 {
			sel = 0
		} else if sel >= shown {
			sel = shown - 1
		}

		availLines := 16
		if h < 20 {
			availLines = h - 4
		}
		if availLines < 1 {
			availLines = 1
		}

		// Adjust offset to ensure sel is within the visible area
		for {
			if sel < offset {
				offset = sel
			}
			visCount := 0
			linesUsed := 0
			for i := offset; i < shown; i++ {
				needed := 1
				if filtered[i].MatchedLine != "" {
					needed = 2
				}
				if linesUsed+needed > availLines && visCount > 0 {
					break
				}
				linesUsed += needed
				visCount++
			}
			if shown == 0 || sel < offset+visCount {
				break
			}
			offset++
		}

		// Move up to the start of our UI
		if linesDrawn > 0 {
			fmt.Printf("\r\033[%dA", linesDrawn)
		}
		fmt.Print("\r")

		linesThisFrame := 0

		// Row 1: query input
		queryPlainW := len([]rune(query))
		countStr := fmt.Sprintf("%s%d/%d%s", cDim, shown, total, cReset)
		countPlain := len(fmt.Sprintf("%d/%d", shown, total))
		pad := w - queryPlainW - countPlain
		if pad < 1 {
			pad = 1
		}
		fmt.Printf("%s%s%s%s\r\n", escClearLine, query, strings.Repeat(" ", pad), countStr)
		linesThisFrame++

		// Row 2: separator
		fmt.Printf("%s  %s%s%s\r\n", escClearLine, cDim, strings.Repeat("─", w-4), cReset)
		linesThisFrame++

		// Results: render only actual items
		visCount := 0
		linesUsed := 0
		for i := offset; i < shown; i++ {
			needed := 1
			if filtered[i].MatchedLine != "" {
				needed = 2
			}
			if linesUsed+needed > availLines && visCount > 0 {
				break
			}
			linesUsed += needed
			visCount++
		}

		for i := 0; i < visCount; i++ {
			idx := offset + i
			b := filtered[idx]
			fName := filepath.Base(b.File)
			fName = strings.TrimSuffix(fName, ".log")
			meta := fmt.Sprintf("%s · L%d", fName, b.LineNum)
			metaW := len([]rune(meta))

			cmdW := w - 4 - 2 - metaW // 4 for "  ▸ ", 2 for gap before meta
			if cmdW < 10 {
				cmdW = 10
			}

			coloredCmd := truncANSI(b.RawCommand, cmdW)
			cmdVis := visibleLen(coloredCmd)
			cmdPad := cmdW - cmdVis
			if cmdPad < 0 {
				cmdPad = 0
			}

			if idx == sel {
				selCmd := truncStr(b.Command, cmdW)
				selPad := cmdW - len([]rune(selCmd))
				if selPad < 0 {
					selPad = 0
				}
				
				// Line 1: Command
				content1 := fmt.Sprintf("  %s▸ %s%s  %s", cFgSel, selCmd, strings.Repeat(" ", selPad), meta)
				visW1 := 4 + len([]rune(selCmd)) + selPad + 2 + len([]rune(meta))
				trailPad1 := w - visW1
				if trailPad1 < 0 { trailPad1 = 0 }
				fmt.Printf("%s%s%s%s%s%s\r\n", escClearLine, cBgSel, cFgSel, content1, strings.Repeat(" ", trailPad1), cReset)
				linesThisFrame++

				// Line 2: MatchedLine
				if b.MatchedLine != "" {
					matchW := w - 6
					if matchW < 10 { matchW = 10 }
					truncMatch := truncStr("↳ "+b.MatchedLine, matchW)
					matchPad := matchW - len([]rune(truncMatch))
					if matchPad < 0 { matchPad = 0 }
					
					content2 := fmt.Sprintf("      %s%s", truncMatch, strings.Repeat(" ", matchPad))
					visW2 := 6 + len([]rune(truncMatch)) + matchPad
					trailPad2 := w - visW2
					if trailPad2 < 0 { trailPad2 = 0 }
					
					fmt.Printf("%s%s%s%s%s%s\r\n", escClearLine, cBgSel, cFgSel, content2, strings.Repeat(" ", trailPad2), cReset)
					linesThisFrame++
				}
			} else {
				// Line 1: Command
				fmt.Printf("%s    %s%s  %s%s%s\r\n", escClearLine,
					coloredCmd, strings.Repeat(" ", cmdPad),
					cDim, meta, cReset)
				linesThisFrame++

				// Line 2: MatchedLine
				if b.MatchedLine != "" {
					matchW := w - 6
					if matchW < 10 { matchW = 10 }
					truncMatch := truncStr("↳ "+b.MatchedLine, matchW)
					fmt.Printf("%s      %s%s%s\r\n", escClearLine, cDim, truncMatch, cReset)
					linesThisFrame++
				}
			}
		}

		// Status bar: immediately after last result
		from := offset + 1
		to := offset + visCount
		if shown == 0 {
			from = 0
			to = 0
		}
		status := fmt.Sprintf(" Items %d to %d of %d ", from, to, shown)
		fmt.Printf("%s\033[46m\033[1m\033[37m%s\033[0m\033[J", escClearLine, status)
		
		linesDrawn = linesThisFrame

		// ── input ─────────────────────────────────────────────
		key := readKey()

		switch key {
		case "esc", "ctrl-c":
			if linesDrawn > 0 {
				fmt.Printf("\r\033[%dA", linesDrawn)
			}
			fmt.Print("\033[1A\r\033[J") // Clear prompt and UI
			return
		case "enter":
			if shown > 0 && sel < shown {
				cmd := filtered[sel]
				if linesDrawn > 0 {
					fmt.Printf("\r\033[%dA", linesDrawn)
				}
				fmt.Print("\033[1A\r\033[J") // Clear prompt and UI
				restore()
				for _, line := range readBlockLines(cmd) {
					fmt.Println(line)
				}
				os.Exit(0)
			}

		case "up", "ctrl-p":
			if sel > 0 {
				sel--
			}
		case "down", "ctrl-n":
			if sel < shown-1 {
				sel++
			}

		case "backspace":
			if len(query) > 0 {
				r := []rune(query)
				query = string(r[:len(r)-1])
				sel = 0
				offset = 0
			}

		case "ctrl-u":
			query = ""
			sel = 0
			offset = 0

		default:
			if len(key) > 0 && key[0] >= 32 {
				query += key
				sel = 0
				offset = 0
			}
		}
	}
}
