package imagecheck

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// instruction is one Dockerfile instruction, continuation lines joined.
type instruction struct {
	line int    // where it starts, from 1
	cmd  string // upper case
	args string
}

// dockerfile is a parsed Dockerfile: its parser directives and its instructions.
type dockerfile struct {
	directives   map[string]string // lower-case keys
	instructions []instruction
}

var directiveRE = regexp.MustCompile(`^#\s*([a-zA-Z]+)\s*=\s*(\S*)\s*$`)

// parseDockerfile reads what the rules need; heredocs aren't supported.
func parseDockerfile(src string) (dockerfile, error) {
	df := dockerfile{directives: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	i := 0
	// parser directives count only before any other line
	for ; i < len(lines); i++ {
		m := directiveRE.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		df.directives[strings.ToLower(m[1])] = m[2]
	}
	if e, ok := df.directives["escape"]; ok && e != `\` {
		return df, fmt.Errorf("escape=%s: only the default escape character is supported", e)
	}

	var cur *instruction
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue // comments and blank lines may also sit inside a continuation
		}
		cont := strings.HasSuffix(t, `\`)
		t = strings.TrimSpace(strings.TrimSuffix(t, `\`))
		if cur == nil {
			cmd, args, _ := strings.Cut(t, " ")
			cur = &instruction{line: i + 1, cmd: strings.ToUpper(cmd), args: strings.TrimSpace(args)}
		} else {
			cur.args += " " + t
		}
		if !cont {
			df.instructions = append(df.instructions, *cur)
			cur = nil
		}
	}
	if cur != nil {
		return df, fmt.Errorf("line %d: a continuation runs past the end", cur.line)
	}
	return df, nil
}

// flags splits an instruction's leading --flags from the rest.
func flags(args string) (map[string]string, []string) {
	f := map[string]string{}
	words := strings.Fields(args)
	for len(words) > 0 && strings.HasPrefix(words[0], "--") {
		k, v, _ := strings.Cut(strings.TrimPrefix(words[0], "--"), "=")
		f[k] = v
		words = words[1:]
	}
	return f, words
}

var digestRE = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// pinned reports whether an image reference names its content by digest.
func pinned(ref string) bool {
	return strings.EqualFold(ref, "scratch") || (!strings.Contains(ref, "$") && digestRE.MatchString(ref))
}

// epoch returns SOURCE_DATE_EPOCH's value from the ARG instructions before the first FROM.
func (df dockerfile) epoch() (int64, error) {
	for _, in := range df.instructions {
		if in.cmd == "FROM" {
			break
		}
		if in.cmd != "ARG" {
			continue
		}
		for w := range strings.FieldsSeq(in.args) {
			if v, ok := strings.CutPrefix(w, "SOURCE_DATE_EPOCH="); ok {
				n, err := strconv.ParseInt(strings.Trim(v, `"`), 10, 64)
				if err != nil || n <= 0 {
					return 0, fmt.Errorf("line %d: SOURCE_DATE_EPOCH=%s is not a positive number of seconds", in.line, v)
				}
				return n, nil
			}
		}
	}
	return 0, errors.New("no ARG SOURCE_DATE_EPOCH=<value> before the first FROM")
}

// dockerfileResults checks the Dockerfile's source against the rules a reproducible image follows.
func dockerfileResults(df dockerfile) []Result {
	_, epochErr := df.epoch()
	res := []Result{{Name: "Dockerfile: SOURCE_DATE_EPOCH before the first FROM", Advice: epochErr}}

	var syntax error
	if s, ok := df.directives["syntax"]; ok && !pinned(s) {
		syntax = fmt.Errorf("# syntax=%s swaps the frontend; pin it by digest or drop it", s)
	}
	res = append(res, Result{Name: "Dockerfile: no unpinned syntax directive", Advice: syntax})

	stages := map[string]bool{}
	isStage := func(ref string) bool {
		_, err := strconv.Atoi(ref)
		return err == nil || stages[strings.ToLower(ref)]
	}
	var unpinned, noChmod, add []error
	for _, in := range df.instructions {
		f, words := flags(in.args)
		switch in.cmd {
		case "FROM":
			if len(words) == 0 {
				unpinned = append(unpinned, fmt.Errorf("line %d: FROM without an image", in.line))
				continue
			}
			if !isStage(words[0]) && !pinned(words[0]) {
				unpinned = append(unpinned, fmt.Errorf("line %d: FROM %s", in.line, words[0]))
			}
			if len(words) == 3 && strings.EqualFold(words[1], "AS") {
				stages[strings.ToLower(words[2])] = true
			}
		case "COPY":
			from, ok := f["from"]
			switch {
			case ok && !isStage(from) && !pinned(from):
				unpinned = append(unpinned, fmt.Errorf("line %d: COPY --from=%s", in.line, from))
			case !ok && f["chmod"] == "":
				noChmod = append(noChmod, fmt.Errorf("line %d: COPY %s", in.line, strings.Join(words, " ")))
			}
		case "RUN":
			for w := range strings.FieldsSeq(in.args) {
				if !strings.HasPrefix(w, "--") {
					break
				}
				v, ok := strings.CutPrefix(w, "--mount=")
				if !ok {
					continue
				}
				for kv := range strings.SplitSeq(v, ",") {
					if from, ok := strings.CutPrefix(kv, "from="); ok && !isStage(from) && !pinned(from) {
						unpinned = append(unpinned, fmt.Errorf("line %d: RUN --mount from=%s", in.line, from))
					}
				}
			}
		case "ADD":
			add = append(add, fmt.Errorf("line %d: ADD; use COPY", in.line))
		}
	}
	return append(res,
		Result{Name: "Dockerfile: every image pinned by digest", Advice: errors.Join(unpinned...)},
		Result{Name: "Dockerfile: --chmod on every COPY from the build context", Advice: errors.Join(noChmod...)},
		Result{Name: "Dockerfile: no ADD", Advice: errors.Join(add...)},
	)
}
