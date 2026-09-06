package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// readDotenv parses a .env file into a map. A missing file is not an error: a
// container is configured entirely through the process environment, and
// requiring the file would make the image refuse to start without one.
func readDotenv(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path) //nolint:gosec // operator-supplied config path, by design
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only handle

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		key, value, ok := parseDotenvLine(scanner.Text())
		if !ok {
			continue
		}
		if key == "" {
			return nil, fmt.Errorf("%s:%d: assignment with an empty name", path, line)
		}
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return out, nil
}

// parseDotenvLine returns the key and value of one .env line, reporting false
// for blanks and comments. It handles the forms the AIR .env files actually
// use: KEY=value, `export KEY=value`, quoted values, and a trailing `# comment`
// after an unquoted value (".env.example" documents several settings that way,
// and treating the comment as part of the value would silently misconfigure
// things like LOG__RENDER).
func parseDotenvLine(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)

	if len(value) >= 2 {
		if q := value[0]; (q == '"' || q == '\'') && value[len(value)-1] == q {
			// Quoted: the quotes delimit the value, so anything inside them —
			// including a '#' — is data.
			return key, value[1 : len(value)-1], true
		}
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return key, value, true
}

// lookupEnv resolves name from the process environment first, then the parsed
// .env file. An empty exported value counts as set, matching the shell's own
// notion of an exported variable.
func lookupEnv(name string, file map[string]string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	v, ok := file[name]
	return v, ok
}
